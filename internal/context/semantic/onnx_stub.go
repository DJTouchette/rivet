//go:build !onnx

package semantic

import "fmt"

// newONNXEmbedder is the default (no-CGo) stub. The bundled-model path is real
// code in onnx.go, compiled only with `-tags onnx`, because it needs the
// ONNX Runtime C library and a model file on disk — neither of which should be
// a hard build dependency for everyone who builds rivet. Without the tag, an
// `onnx` backend reports unavailable and the caller falls back to lexical.
//
// To enable it:
//
//	go get github.com/yalue/onnxruntime_go
//	# install libonnxruntime (see that repo), drop a model dir on disk, then:
//	go build -tags onnx ./...
//
// See onnx.go for the model-file contract.
func newONNXEmbedder(cfg Config) (Embedder, error) {
	return nil, fmt.Errorf("%w: built without `onnx` tag (model=%q); rebuild with -tags onnx or use an HTTP backend", ErrUnavailable, cfg.Model)
}
