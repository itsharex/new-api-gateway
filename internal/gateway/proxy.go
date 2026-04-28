package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/your-company/new-api-gateway/internal/alerts"
	"github.com/your-company/new-api-gateway/internal/authkeys"
	"github.com/your-company/new-api-gateway/internal/evidence"
	"github.com/your-company/new-api-gateway/internal/fingerprint"
	"github.com/your-company/new-api-gateway/internal/identity"
	"github.com/your-company/new-api-gateway/internal/ids"
	"github.com/your-company/new-api-gateway/internal/jobs"
	"github.com/your-company/new-api-gateway/internal/routes"
	"github.com/your-company/new-api-gateway/internal/traces"
)

type IdentityResolver interface {
	Resolve(ctx context.Context, canonicalKey, fingerprintValue, fingerprintDisplay string) (identity.Snapshot, error)
}

type Handler struct {
	UpstreamBaseURL           string
	Registry                  routes.Registry
	EvidenceStore             evidence.Store
	TraceRepo                 traces.Repository
	IdentityResolver          IdentityResolver
	AuditSecret               string
	Client                    *http.Client
	Now                       func() time.Time
	AuditError                func(ctx context.Context, err error)
	MaxRequestBodyBytes       int64
	AuditTimeout              time.Duration
	WebSocketHandshakeTimeout time.Duration
	JobPublisher              jobs.Publisher
	CoverageEmitter           alerts.Emitter
}

const (
	defaultAuditTimeout              = 5 * time.Second
	defaultWebSocketHandshakeTimeout = 10 * time.Second
)

func (h Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	startedAt := h.now()
	traceID := ids.NewTraceID()

	entry, ok := h.Registry.Match(req.Method, req.URL.Path)
	unknownRoute := !ok
	if !ok {
		entry = routes.Entry{
			Method:         req.Method,
			PathPattern:    req.URL.Path,
			ProtocolFamily: "unknown",
			CaptureMode:    routes.CaptureRawOnly,
		}
	}

	capturedReq, err := captureRequestBody(req, h.maxRequestBodyBytes())
	if err != nil {
		if errors.Is(err, ErrRequestBodyTooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	authResult, hasAuth := authkeys.Extract(req)
	if hasAuth && len(h.AuditSecret) < 32 {
		http.Error(w, "audit secret is not configured", http.StatusInternalServerError)
		return
	}
	snapshot := h.resolveIdentity(req.Context(), authResult, hasAuth)

	auditCtx, cancelAudit := h.auditContext(req.Context())
	requestObject, err := h.putEvidence(auditCtx, traceID, "request_body", capturedReq.ContentType, capturedReq.BodyBytes)
	if err != nil {
		h.reportAuditError(auditCtx, err)
		cancelAudit()
		http.Error(w, "failed to store request evidence", http.StatusInternalServerError)
		return
	}
	cancelAudit()

	auditCtx, cancelAudit = h.auditContext(req.Context())
	requestHeadersObject, err := h.putHeaderEvidence(auditCtx, traceID, "request_headers", req.Header)
	if err != nil {
		h.reportAuditError(auditCtx, err)
		cancelAudit()
		http.Error(w, "failed to store request header evidence", http.StatusInternalServerError)
		return
	}
	cancelAudit()

	modelRequested := extractRequestModel(req.URL.Path, capturedReq.BodyBytes)

	if entry.BodyKind == "websocket" && isWebSocketUpgrade(req) {
		h.serveWebSocketTunnel(w, req, traceRecord{
			traceID:              traceID,
			req:                  req,
			entry:                entry,
			statusCode:           http.StatusSwitchingProtocols,
			upstreamCode:         http.StatusSwitchingProtocols,
			startedAt:            startedAt,
			requestObject:        requestObject,
			requestHeadersObject: requestHeadersObject,
			requestContentType:   capturedReq.ContentType,
			modelRequested:       modelRequested,
			requestSize:          capturedReq.SizeBytes,
			snapshot:             snapshot,
			stream:               true,
			unknownRoute:         unknownRoute,
		})
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
		auditCtx, cancelAudit := h.auditContext(req.Context())
		defer cancelAudit()
		h.insertTrace(auditCtx, traceRecord{
			traceID:              traceID,
			req:                  req,
			entry:                entry,
			statusCode:           http.StatusBadGateway,
			upstreamCode:         0,
			startedAt:            startedAt,
			finishedAt:           finishedAt,
			requestObject:        requestObject,
			requestHeadersObject: requestHeadersObject,
			requestContentType:   capturedReq.ContentType,
			modelRequested:       modelRequested,
			requestSize:          capturedReq.SizeBytes,
			snapshot:             snapshot,
			unknownRoute:         unknownRoute,
		})
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer upstreamResp.Body.Close()

	if isStreamingResponse(upstreamResp) {
		h.serveStreamingResponse(w, req, upstreamResp, traceRecord{
			traceID:              traceID,
			req:                  req,
			entry:                entry,
			statusCode:           upstreamResp.StatusCode,
			upstreamCode:         upstreamResp.StatusCode,
			startedAt:            startedAt,
			requestObject:        requestObject,
			requestHeadersObject: requestHeadersObject,
			requestContentType:   capturedReq.ContentType,
			modelRequested:       modelRequested,
			requestSize:          capturedReq.SizeBytes,
			snapshot:             snapshot,
			stream:               true,
			unknownRoute:         unknownRoute,
		})
		return
	}

	responseBody, err := io.ReadAll(upstreamResp.Body)
	if err != nil {
		finishedAt := h.now()
		auditCtx, cancelAudit := h.auditContext(req.Context())
		defer cancelAudit()
		h.reportAuditError(auditCtx, err)
		responseHeadersObject, headerErr := h.putHeaderEvidence(auditCtx, traceID, "response_headers", upstreamResp.Header)
		if headerErr != nil {
			h.reportAuditError(auditCtx, headerErr)
		}
		_ = h.insertTrace(auditCtx, traceRecord{
			traceID:               traceID,
			req:                   req,
			entry:                 entry,
			statusCode:            http.StatusBadGateway,
			upstreamCode:          upstreamResp.StatusCode,
			startedAt:             startedAt,
			finishedAt:            finishedAt,
			requestObject:         requestObject,
			requestHeadersObject:  requestHeadersObject,
			responseHeadersObject: responseHeadersObject,
			requestContentType:    capturedReq.ContentType,
			responseContentType:   upstreamResp.Header.Get("Content-Type"),
			modelRequested:        modelRequested,
			requestSize:           capturedReq.SizeBytes,
			snapshot:              snapshot,
			unknownRoute:          unknownRoute,
			skipPostPersistence:   true,
		})
		http.Error(w, "failed to read upstream response", http.StatusBadGateway)
		return
	}
	finishedAt := h.now()

	auditCtx, cancelAudit = h.auditContext(req.Context())
	defer cancelAudit()
	responseObject, err := h.putEvidence(auditCtx, traceID, "response_body", upstreamResp.Header.Get("Content-Type"), responseBody)
	skipPostPersistence := false
	if err != nil {
		h.reportAuditError(auditCtx, err)
		skipPostPersistence = true
	}
	responseHeadersObject, headerErr := h.putHeaderEvidence(auditCtx, traceID, "response_headers", upstreamResp.Header)
	if headerErr != nil {
		h.reportAuditError(auditCtx, headerErr)
		skipPostPersistence = true
	}
	usage := extractResponseUsage(responseBody)

	_ = h.insertTrace(auditCtx, traceRecord{
		traceID:               traceID,
		req:                   req,
		entry:                 entry,
		statusCode:            upstreamResp.StatusCode,
		upstreamCode:          upstreamResp.StatusCode,
		startedAt:             startedAt,
		finishedAt:            finishedAt,
		requestObject:         requestObject,
		responseObject:        responseObject,
		requestHeadersObject:  requestHeadersObject,
		responseHeadersObject: responseHeadersObject,
		requestContentType:    capturedReq.ContentType,
		responseContentType:   upstreamResp.Header.Get("Content-Type"),
		modelRequested:        modelRequested,
		usage:                 usage,
		requestSize:           capturedReq.SizeBytes,
		responseSize:          int64(len(responseBody)),
		snapshot:              snapshot,
		unknownRoute:          unknownRoute,
		skipPostPersistence:   skipPostPersistence,
	})

	copyHeaders(w.Header(), upstreamResp.Header)
	w.Header().Set("x-audit-trace-id", traceID)
	w.WriteHeader(upstreamResp.StatusCode)
	_, _ = w.Write(responseBody)
}

type traceRecord struct {
	traceID               string
	req                   *http.Request
	entry                 routes.Entry
	statusCode            int
	upstreamCode          int
	startedAt             time.Time
	finishedAt            time.Time
	requestObject         evidence.Object
	responseObject        evidence.Object
	requestHeadersObject  evidence.Object
	responseHeadersObject evidence.Object
	requestContentType    string
	responseContentType   string
	modelRequested        string
	usage                 minimalUsage
	requestSize           int64
	responseSize          int64
	snapshot              identity.Snapshot
	stream                bool
	unknownRoute          bool
	skipPostPersistence   bool
}

func (h Handler) insertTrace(ctx context.Context, record traceRecord) error {
	if h.TraceRepo == nil {
		return nil
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
		Stream:                   record.stream,
		RequestStartedAt:         record.startedAt,
		ResponseFinishedAt:       record.finishedAt,
		DurationMillis:           record.finishedAt.Sub(record.startedAt).Milliseconds(),
		RequestBodySize:          record.requestSize,
		ResponseBodySize:         record.responseSize,
		RequestBodySHA256:        record.requestObject.SHA256,
		ResponseBodySHA256:       record.responseObject.SHA256,
		RequestRawRef:            record.requestObject.ObjectRef,
		RequestHeadersRef:        record.requestHeadersObject.ObjectRef,
		ResponseRawRef:           record.responseObject.ObjectRef,
		ResponseHeadersRef:       record.responseHeadersObject.ObjectRef,
		TokenFingerprint:         record.snapshot.TokenFingerprint,
		FingerprintDisplay:       record.snapshot.FingerprintDisplay,
		NewAPITokenIDSnapshot:    record.snapshot.NewAPITokenID,
		TokenNameSnapshot:        record.snapshot.TokenNameRaw,
		EmployeeNoSnapshot:       record.snapshot.EmployeeNo,
		IdentityResolutionStatus: record.snapshot.ResolutionStatus,
		IdentityCacheStatus:      record.snapshot.IdentityCacheStatus,
		ModelRequested:           record.modelRequested,
		UsagePromptTokens:        record.usage.PromptTokens,
		UsageCompletionTokens:    record.usage.CompletionTokens,
		UsageTotalTokens:         record.usage.TotalTokens,
		UsageReasoningTokens:     record.usage.ReasoningTokens,
		UsageCachedTokens:        record.usage.CachedTokens,
		AnalysisStatus:           "pending",
		CreatedAt:                record.startedAt,
	}
	var errs []error
	if err := h.TraceRepo.InsertTrace(ctx, trace); err != nil {
		h.reportAuditError(ctx, err)
		errs = append(errs, err)
		return errors.Join(errs...)
	}

	if err := h.insertEvidenceObject(ctx, record.traceID, "request_body", record.requestObject); err != nil {
		errs = append(errs, err)
	}
	if record.requestHeadersObject.ObjectRef != "" {
		if err := h.insertEvidenceObject(ctx, record.traceID, "request_headers", record.requestHeadersObject); err != nil {
			errs = append(errs, err)
		}
	}
	if record.responseObject.ObjectRef != "" {
		if err := h.insertEvidenceObject(ctx, record.traceID, "response_body", record.responseObject); err != nil {
			errs = append(errs, err)
		}
	}
	if record.responseHeadersObject.ObjectRef != "" {
		if err := h.insertEvidenceObject(ctx, record.traceID, "response_headers", record.responseHeadersObject); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if record.skipPostPersistence {
		return nil
	}

	if err := h.emitCoverageAlert(ctx, record); err != nil {
		errs = append(errs, err)
	}
	if h.JobPublisher != nil {
		job := jobs.NewTraceCaptured(jobs.TraceCapturedInput{
			TraceID:               record.traceID,
			RoutePattern:          record.entry.PathPattern,
			ProtocolFamily:        record.entry.ProtocolFamily,
			CaptureMode:           string(record.entry.CaptureMode),
			EmployeeNo:            record.snapshot.EmployeeNo,
			TokenFingerprint:      record.snapshot.TokenFingerprint,
			FingerprintDisplay:    record.snapshot.FingerprintDisplay,
			NewAPITokenID:         record.snapshot.NewAPITokenID,
			TokenNameSnapshot:     record.snapshot.TokenNameRaw,
			StatusCode:            record.statusCode,
			UpstreamStatusCode:    record.upstreamCode,
			Stream:                record.stream,
			RequestStartedAt:      record.startedAt.UTC().Format(time.RFC3339),
			RequestBodySize:       record.requestSize,
			ResponseBodySize:      record.responseSize,
			RequestRawRef:         record.requestObject.ObjectRef,
			RequestHeadersRef:     record.requestHeadersObject.ObjectRef,
			ResponseRawRef:        record.responseObject.ObjectRef,
			ResponseHeadersRef:    record.responseHeadersObject.ObjectRef,
			RequestContentType:    record.requestContentType,
			ResponseContentType:   record.responseContentType,
			ModelRequested:        record.modelRequested,
			UsagePromptTokens:     record.usage.PromptTokens,
			UsageCompletionTokens: record.usage.CompletionTokens,
			UsageTotalTokens:      record.usage.TotalTokens,
			UsageReasoningTokens:  record.usage.ReasoningTokens,
			UsageCachedTokens:     record.usage.CachedTokens,
		})
		if err := h.JobPublisher.PublishTraceCaptured(ctx, job); err != nil {
			h.reportAuditError(ctx, err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (h Handler) serveWebSocketTunnel(w http.ResponseWriter, req *http.Request, record traceRecord) {
	upstreamReq, err := h.newWebSocketUpstreamRequest(req)
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusBadGateway)
		h.recordWebSocketTrace(req, record, http.StatusBadGateway, 0)
		return
	}
	upstreamConn, err := dialUpstream(req.Context(), upstreamReq.URL)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		h.recordWebSocketTrace(req, record, http.StatusBadGateway, 0)
		return
	}
	defer upstreamConn.Close()

	upstreamResp, upstreamReader, err := h.readWebSocketUpstreamHandshake(req.Context(), upstreamConn, upstreamReq)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		h.recordWebSocketTrace(req, record, http.StatusBadGateway, 0)
		return
	}
	record.statusCode = upstreamResp.StatusCode
	record.upstreamCode = upstreamResp.StatusCode
	headerCtx, cancelHeaders := h.auditContext(req.Context())
	responseHeadersObject, headerErr := h.putHeaderEvidence(headerCtx, record.traceID, "response_headers", upstreamResp.Header)
	cancelHeaders()
	if headerErr != nil {
		h.reportAuditError(req.Context(), headerErr)
		record.skipPostPersistence = true
	} else {
		record.responseHeadersObject = responseHeadersObject
	}
	record.responseContentType = upstreamResp.Header.Get("Content-Type")

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket tunnel unsupported", http.StatusInternalServerError)
		h.recordWebSocketTrace(req, record, http.StatusInternalServerError, upstreamResp.StatusCode)
		return
	}
	clientConn, clientRW, err := hijacker.Hijack()
	if err != nil {
		h.reportAuditError(req.Context(), err)
		h.recordWebSocketTrace(req, record, http.StatusInternalServerError, upstreamResp.StatusCode)
		return
	}
	defer clientConn.Close()

	if err := upstreamResp.Write(clientConn); err != nil {
		h.reportAuditError(req.Context(), err)
		h.recordWebSocketTrace(req, record, http.StatusBadGateway, upstreamResp.StatusCode)
		return
	}
	if upstreamResp.StatusCode == http.StatusSwitchingProtocols {
		copyBidirectional(clientConn, clientRW.Reader, upstreamConn, upstreamReader)
	}
	h.recordWebSocketTrace(req, record, record.statusCode, record.upstreamCode)
}

func (h Handler) recordWebSocketTrace(req *http.Request, record traceRecord, statusCode, upstreamCode int) {
	record.statusCode = statusCode
	record.upstreamCode = upstreamCode
	record.finishedAt = h.now()
	auditCtx, cancelAudit := h.auditContext(req.Context())
	defer cancelAudit()
	_ = h.insertTrace(auditCtx, record)
}

func (h Handler) newWebSocketUpstreamRequest(req *http.Request) (*http.Request, error) {
	target, err := h.upstreamURL(req.URL)
	if err != nil {
		return nil, err
	}
	upstreamReq, err := http.NewRequestWithContext(req.Context(), req.Method, target.String(), nil)
	if err != nil {
		return nil, err
	}
	upstreamReq.Header = req.Header.Clone()
	stripHopByHopHeaders(upstreamReq.Header)
	upstreamReq.Header.Set("Connection", "Upgrade")
	upstreamReq.Header.Set("Upgrade", "websocket")
	upstreamReq.Host = target.Host
	return upstreamReq, nil
}

func (h Handler) readWebSocketUpstreamHandshake(ctx context.Context, upstreamConn net.Conn, upstreamReq *http.Request) (*http.Response, *bufio.Reader, error) {
	if err := upstreamConn.SetDeadline(time.Now().Add(h.websocketHandshakeTimeout())); err != nil {
		return nil, nil, err
	}
	handshakeDone := make(chan struct{})
	defer close(handshakeDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = upstreamConn.Close()
		case <-handshakeDone:
		}
	}()

	if err := upstreamReq.Write(upstreamConn); err != nil {
		return nil, nil, err
	}
	upstreamReader := bufio.NewReader(upstreamConn)
	upstreamResp, err := http.ReadResponse(upstreamReader, upstreamReq)
	if err != nil {
		return nil, nil, err
	}
	if err := upstreamConn.SetDeadline(time.Time{}); err != nil {
		_ = upstreamResp.Body.Close()
		return nil, nil, err
	}
	return upstreamResp, upstreamReader, nil
}

func (h Handler) websocketHandshakeTimeout() time.Duration {
	if h.WebSocketHandshakeTimeout > 0 {
		return h.WebSocketHandshakeTimeout
	}
	return defaultWebSocketHandshakeTimeout
}

func dialUpstream(ctx context.Context, target *url.URL) (net.Conn, error) {
	host := target.Hostname()
	port := target.Port()
	switch target.Scheme {
	case "http":
		if port == "" {
			port = "80"
		}
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	case "https":
		if port == "" {
			port = "443"
		}
		dialer := tls.Dialer{Config: &tls.Config{ServerName: host}}
		return dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	default:
		return nil, errors.New("unsupported upstream scheme")
	}
}

func copyBidirectional(clientConn net.Conn, clientReader *bufio.Reader, upstreamConn net.Conn, upstreamReader *bufio.Reader) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(upstreamConn, io.MultiReader(clientReader, clientConn))
		_ = upstreamConn.Close()
		_ = clientConn.Close()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, io.MultiReader(upstreamReader, upstreamConn))
		_ = clientConn.Close()
		_ = upstreamConn.Close()
	}()
	wg.Wait()
}

func (h Handler) serveStreamingResponse(w http.ResponseWriter, req *http.Request, upstreamResp *http.Response, record traceRecord) {
	headerCtx, cancelHeaders := h.auditContext(req.Context())
	responseHeadersObject, headerErr := h.putHeaderEvidence(headerCtx, record.traceID, "response_headers", upstreamResp.Header)
	cancelHeaders()
	if headerErr != nil {
		h.reportAuditError(req.Context(), headerErr)
		record.skipPostPersistence = true
	}
	record.responseHeadersObject = responseHeadersObject
	record.responseContentType = upstreamResp.Header.Get("Content-Type")

	copyHeaders(w.Header(), upstreamResp.Header)
	w.Header().Set("x-audit-trace-id", record.traceID)
	w.WriteHeader(upstreamResp.StatusCode)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	var responseObject evidence.Object
	var responseErr error
	var captureErr error
	var storeErr error
	clientWriter := flushWriter{writer: w}
	if flusher, ok := w.(http.Flusher); ok {
		clientWriter.flusher = flusher
	}

	var written int64
	if h.EvidenceStore == nil {
		written, responseErr, captureErr = copyStreamToClientAndCapture(upstreamResp.Body, clientWriter, nil)
	} else {
		captureCtx, cancelCapture := context.WithCancel(context.WithoutCancel(req.Context()))
		defer cancelCapture()
		pr, pw := io.Pipe()
		storeDone := make(chan struct {
			object evidence.Object
			err    error
		}, 1)
		go func() {
			defer pr.Close()
			object, err := h.EvidenceStore.Put(captureCtx, evidence.PutRequest{
				TraceID:     record.traceID,
				ObjectType:  "response_body",
				ContentType: upstreamResp.Header.Get("Content-Type"),
				Reader:      pr,
			})
			storeDone <- struct {
				object evidence.Object
				err    error
			}{object: object, err: err}
		}()

		written, responseErr, captureErr = copyStreamToClientAndCapture(upstreamResp.Body, clientWriter, pw)
		if responseErr != nil {
			_ = pw.CloseWithError(responseErr)
		} else {
			_ = pw.Close()
		}
		result := <-storeDone
		responseObject = result.object
		storeErr = result.err
	}
	auditCtx, cancelAudit := h.auditContext(req.Context())
	defer cancelAudit()

	if storeErr != nil {
		h.reportAuditError(auditCtx, storeErr)
		record.skipPostPersistence = true
	} else if captureErr != nil {
		h.reportAuditError(auditCtx, captureErr)
		record.skipPostPersistence = true
	}
	if responseErr != nil {
		h.reportAuditError(auditCtx, responseErr)
	}

	record.finishedAt = h.now()
	record.responseObject = responseObject
	record.responseSize = written
	_ = h.insertTrace(auditCtx, record)
}

func (h Handler) auditContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := h.AuditTimeout
	if timeout <= 0 {
		timeout = defaultAuditTimeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func (h Handler) emitCoverageAlert(ctx context.Context, record traceRecord) error {
	if h.CoverageEmitter == nil {
		return nil
	}
	var alert alerts.CoverageAlert
	switch {
	case record.unknownRoute:
		alert = alerts.UnknownRoute(record.req.Method, record.req.URL.Path, record.req.Header.Get("Content-Type"), record.traceID)
	case record.entry.UnsupportedAlertCode == "known_route_raw_first":
		alert = alerts.KnownRawFirst(record.req.Method, record.entry.PathPattern, record.req.URL.Path, record.entry.ProtocolFamily, record.traceID)
	default:
		return nil
	}
	if err := h.CoverageEmitter.EmitCoverageAlert(ctx, alert); err != nil {
		h.reportAuditError(ctx, err)
		return err
	}
	return nil
}

func (h Handler) insertEvidenceObject(ctx context.Context, traceID, objectType string, object evidence.Object) error {
	if object.CreatedAt.IsZero() {
		return nil
	}
	err := h.TraceRepo.InsertRawEvidence(ctx, traces.RawEvidenceObject{
		TraceID:        traceID,
		ObjectType:     objectType,
		ObjectRef:      object.ObjectRef,
		StorageBackend: object.StorageBackend,
		ContentType:    object.ContentType,
		SizeBytes:      object.SizeBytes,
		SHA256:         object.SHA256,
		CreatedAt:      object.CreatedAt,
	})
	if err != nil {
		h.reportAuditError(ctx, err)
	}
	return err
}

func (h Handler) resolveIdentity(ctx context.Context, result authkeys.Result, hasAuth bool) identity.Snapshot {
	if !hasAuth {
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
	snapshot, err := h.IdentityResolver.Resolve(ctx, result.CanonicalKey, fp.Value, fp.Display)
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

func (h Handler) putHeaderEvidence(ctx context.Context, traceID, objectType string, header http.Header) (evidence.Object, error) {
	data, err := headerEvidenceJSON(header)
	if err != nil {
		return evidence.Object{}, err
	}
	return h.putEvidence(ctx, traceID, objectType, "application/json", data)
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
	stripHopByHopHeaders(upstreamReq.Header)
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

func (h Handler) maxRequestBodyBytes() int64 {
	if h.MaxRequestBodyBytes > 0 {
		return h.MaxRequestBodyBytes
	}
	return DefaultMaxRequestBodyBytes
}

func (h Handler) reportAuditError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	if h.AuditError != nil {
		h.AuditError(ctx, err)
		return
	}
	log.Printf("audit error: %v", err)
}

func copyHeaders(dst, src http.Header) {
	headers := src.Clone()
	stripHopByHopHeaders(headers)
	for key, values := range headers {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func stripHopByHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				delHeader(header, name)
			}
		}
	}
	for _, name := range []string{
		"Connection",
		"Proxy-Connection",
		"Keep-Alive",
		"TE",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	} {
		delHeader(header, name)
	}
}

func isWebSocketUpgrade(req *http.Request) bool {
	return headerContainsToken(req.Header, "Connection", "Upgrade") &&
		strings.EqualFold(req.Header.Get("Upgrade"), "websocket")
}

func headerContainsToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func delHeader(header http.Header, name string) {
	header.Del(name)
	for key := range header {
		if strings.EqualFold(key, name) {
			delete(header, key)
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
