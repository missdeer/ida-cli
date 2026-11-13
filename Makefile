.PHONY: build test fmt vet gen-catalog sync-plugin clean

BINARY := ida-cli.exe

build:
	go build -trimpath -ldflags "-s -w" -o $(BINARY) ./cmd/ida-cli

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

gen-catalog:
	python scripts/gen_catalog.py --source ../ida-pro-mcp-enhancement

sync-plugin:
	python scripts/sync_plugin.py --source ../ida-pro-mcp-enhancement

clean:
	rm -f $(BINARY)
