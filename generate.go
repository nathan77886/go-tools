//go:build tools
// +build tools

package protoc_gen_api_registry

//go:generate protoc -I . proto/annotations/extra.proto --go_out=proto/annotations/
