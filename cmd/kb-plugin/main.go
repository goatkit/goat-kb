// Command kb-plugin serves the GoatFlow Knowledge Base plugin via gRPC (go-plugin).
//
// Build: go build -ldflags="-s -w" -o kb ./cmd/kb-plugin
//
// Deploy:
//
//	plugins/kb/
//	  ├── plugin.yaml   # manifest
//	  └── kb            # this binary
package main

import (
	"github.com/goatkit/goat-kb/internal/kb"
	"github.com/goatkit/goatflow/pkg/plugin/grpcutil"
)

func main() {
	grpcutil.ServePlugin(kb.New())
}
