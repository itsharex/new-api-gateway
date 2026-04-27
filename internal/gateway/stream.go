package gateway

import "io"

func teeStream(src io.Reader, client io.Writer, capture io.Writer) (int64, error) {
	return io.Copy(io.MultiWriter(client, capture), src)
}
