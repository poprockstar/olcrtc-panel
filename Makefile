.PHONY: build backend frontend linux test

build: frontend backend

backend: bin
	go build -o bin/olcpanel ./cmd/olcpanel

linux: bin
	GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel

bin:
	mkdir -p bin

frontend:
	npm --prefix frontend run build

test:
	go test ./...
