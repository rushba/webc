package compress

import (
	"bytes"
	"compress/gzip"
	"sync"
)

var gzipWriterPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(nil)
	},
}

// Gzip compresses data using a pooled gzip writer to reduce GC pressure
func Gzip(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzipWriterPool.Get().(*gzip.Writer)
	gz.Reset(&buf)
	if _, err := gz.Write(data); err != nil {
		gzipWriterPool.Put(gz)
		return nil, err
	}
	if err := gz.Close(); err != nil {
		gzipWriterPool.Put(gz)
		return nil, err
	}
	gzipWriterPool.Put(gz)
	return buf.Bytes(), nil
}
