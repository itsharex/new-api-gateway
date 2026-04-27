package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/your-company/new-api-gateway/internal/authkeys"
	"github.com/your-company/new-api-gateway/internal/evidence"
	"github.com/your-company/new-api-gateway/internal/fingerprint"
	"github.com/your-company/new-api-gateway/internal/identity"
	"github.com/your-company/new-api-gateway/internal/ids"
	"github.com/your-company/new-api-gateway/internal/routes"
	"github.com/your-company/new-api-gateway/internal/traces"
)

type IdentityResolver interface {
	Resolve(ctx context.Context, canonicalKey, fingerprintValue, fingerprintDisplay string) (identity.Snapshot, error)
}

type Handler struct {
	UpstreamBaseURL  string
	Registry         routes.Registry
	EvidenceStore    evidence.Store
	TraceRepo        traces.Repository
	IdentityResolver IdentityResolver
	AuditSecret      string
	Client           *http.Client
	Now              func() time.Time
}

func (h Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	startedAt := h.now()
	traceID := ids.NewTraceID()

	entry, ok := h.Registry.Match(req.Method, req.URL.Path)
	if !ok {
		entry = routes.Entry{
			Method:         req.Method,
			PathPattern:    req.URL.Path,
			ProtocolFamily: "unknown",
			CaptureMode:    routes.CaptureRawOnly,
		}
	}

	capturedReq, err := captureRequestBody(req)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	snapshot := h.resolveIdentity(req)

	requestObject, err := h.putEvidence(req.Context(), traceID, "request_body", capturedReq.ContentType, capturedReq.BodyBytes)
	if err != nil {
		http.Error(w, "failed to store request evidence", http.StatusInternalServerError)
		return
	}

	upstreamReq, err := h.newUpstreamRequest(req, capturedReq.BodyBytes)
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusBadGateway)
		return
	}

	upstreamResp, err := h.client().Do(upstreamReq)
	if err != nil {
		finishedAt := h.now()
		h.insertTrace(req.Context(), traceRecord{
			traceID:       traceID,
			req:           req,
			entry:         entry,
			statusCode:    http.StatusBadGateway,
			upstreamCode:  0,
			startedAt:     startedAt,
			finishedAt:    finishedAt,
			requestObject: requestObject,
			requestSize:   capturedReq.SizeBytes,
			snapshot:      snapshot,
		})
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer upstreamResp.Body.Close()

	responseBody, err := io.ReadAll(upstreamResp.Body)
	if err != nil {
		http.Error(w, "failed to read upstream response", http.StatusBadGateway)
		return
	}
	finishedAt := h.now()

	responseObject, err := h.putEvidence(req.Context(), traceID, "response_body", upstreamResp.Header.Get("Content-Type"), responseBody)
	if err != nil {
		http.Error(w, "failed to store response evidence", http.StatusInternalServerError)
		return
	}

	h.insertTrace(req.Context(), traceRecord{
		traceID:        traceID,
		req:            req,
		entry:          entry,
		statusCode:     upstreamResp.StatusCode,
		upstreamCode:   upstreamResp.StatusCode,
		startedAt:      startedAt,
		finishedAt:     finishedAt,
		requestObject:  requestObject,
		responseObject: responseObject,
		requestSize:    capturedReq.SizeBytes,
		responseSize:   int64(len(responseBody)),
		snapshot:       snapshot,
	})

	copyHeaders(w.Header(), upstreamResp.Header)
	w.Header().Set("x-audit-trace-id", traceID)
	w.WriteHeader(upstreamResp.StatusCode)
	_, _ = w.Write(responseBody)
}

type traceRecord struct {
	traceID        string
	req            *http.Request
	entry          routes.Entry
	statusCode     int
	upstreamCode   int
	startedAt      time.Time
	finishedAt     time.Time
	requestObject  evidence.Object
	responseObject evidence.Object
	requestSize    int64
	responseSize   int64
	snapshot       identity.Snapshot
}

func (h Handler) insertTrace(ctx context.Context, record traceRecord) {
	if h.TraceRepo == nil {
		return
	}
	trace := traces.Trace{
		TraceID:                  record.traceID,
		Method:                   record.req.Method,
		Path:                     record.req.URL.Path,
		RoutePattern:             record.entry.PathPattern,
		ProtocolFamily:           record.entry.ProtocolFamily,
		CaptureMode:              string(record.entry.CaptureMode),
		StatusCode:               record.statusCode,
		UpstreamStatusCode:       record.upstreamCode,
		Stream:                   false,
		RequestStartedAt:         record.startedAt,
		ResponseFinishedAt:       record.finishedAt,
		DurationMillis:           record.finishedAt.Sub(record.startedAt).Milliseconds(),
		RequestBodySize:          record.requestSize,
		ResponseBodySize:         record.responseSize,
		RequestBodySHA256:        record.requestObject.SHA256,
		ResponseBodySHA256:       record.responseObject.SHA256,
		RequestRawRef:            record.requestObject.ObjectRef,
		ResponseRawRef:           record.responseObject.ObjectRef,
		TokenFingerprint:         record.snapshot.TokenFingerprint,
		FingerprintDisplay:       record.snapshot.FingerprintDisplay,
		NewAPITokenIDSnapshot:    record.snapshot.NewAPITokenID,
		TokenNameSnapshot:        record.snapshot.TokenNameRaw,
		EmployeeNoSnapshot:       record.snapshot.EmployeeNo,
		IdentityResolutionStatus: record.snapshot.ResolutionStatus,
		IdentityCacheStatus:      record.snapshot.IdentityCacheStatus,
		AnalysisStatus:           "pending",
		CreatedAt:                record.startedAt,
	}
	_ = h.TraceRepo.InsertTrace(ctx, trace)
	h.insertEvidenceObject(ctx, record.traceID, "request_body", record.requestObject)
	if record.responseObject.ObjectRef != "" {
		h.insertEvidenceObject(ctx, record.traceID, "response_body", record.responseObject)
	}
}

func (h Handler) insertEvidenceObject(ctx context.Context, traceID, objectType string, object evidence.Object) {
	if object.CreatedAt.IsZero() {
		return
	}
	_ = h.TraceRepo.InsertRawEvidence(ctx, traces.RawEvidenceObject{
		TraceID:        traceID,
		ObjectType:     objectType,
		ObjectRef:      object.ObjectRef,
		StorageBackend: object.StorageBackend,
		ContentType:    object.ContentType,
		SizeBytes:      object.SizeBytes,
		SHA256:         object.SHA256,
		CreatedAt:      object.CreatedAt,
	})
}

func (h Handler) resolveIdentity(req *http.Request) identity.Snapshot {
	result, ok := authkeys.Extract(req)
	if !ok {
		return identity.Snapshot{ResolutionStatus: "extract_failed"}
	}
	fp := fingerprint.Compute(result.CanonicalKey, h.AuditSecret)
	if h.IdentityResolver == nil {
		return identity.Snapshot{
			TokenFingerprint:   fp.Value,
			FingerprintDisplay: fp.Display,
			ResolutionStatus:   "resolve_failed",
		}
	}
	snapshot, err := h.IdentityResolver.Resolve(req.Context(), result.CanonicalKey, fp.Value, fp.Display)
	if err != nil {
		return identity.Snapshot{
			TokenFingerprint:   fp.Value,
			FingerprintDisplay: fp.Display,
			ResolutionStatus:   "resolve_failed",
		}
	}
	return snapshot
}

func (h Handler) putEvidence(ctx context.Context, traceID, objectType, contentType string, body []byte) (evidence.Object, error) {
	if h.EvidenceStore == nil {
		return evidence.Object{}, nil
	}
	return h.EvidenceStore.Put(ctx, evidence.PutRequest{
		TraceID:     traceID,
		ObjectType:  objectType,
		ContentType: contentType,
		Reader:      bytes.NewReader(body),
	})
}

func (h Handler) newUpstreamRequest(req *http.Request, body []byte) (*http.Request, error) {
	target, err := h.upstreamURL(req.URL)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := http.NewRequestWithContext(req.Context(), req.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	upstreamReq.Header = req.Header.Clone()
	return upstreamReq, nil
}

func (h Handler) upstreamURL(reqURL *url.URL) (*url.URL, error) {
	base, err := url.Parse(h.UpstreamBaseURL)
	if err != nil {
		return nil, err
	}
	target := *base
	target.Path = joinURLPath(base.Path, reqURL.Path)
	target.RawQuery = reqURL.RawQuery
	target.ForceQuery = reqURL.ForceQuery
	return &target, nil
}

func (h Handler) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return http.DefaultClient
}

func (h Handler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func joinURLPath(basePath, requestPath string) string {
	if basePath == "" {
		return requestPath
	}
	if requestPath == "" {
		return basePath
	}
	baseSlash := strings.HasSuffix(basePath, "/")
	requestSlash := strings.HasPrefix(requestPath, "/")
	switch {
	case baseSlash && requestSlash:
		return basePath + requestPath[1:]
	case !baseSlash && !requestSlash:
		return basePath + "/" + requestPath
	default:
		return basePath + requestPath
	}
}
