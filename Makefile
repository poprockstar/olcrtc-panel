.PHONY: build backend frontend linux test

build: frontend backend

backend:
	go build -o bin/olcpanel ./cmd/olcpanel

linux:
	GOOS=linux GOARCH=amd64 go build -o bin/olcpanel-linux-amd64 ./cmd/olcpanel

frontend:
	npm --prefix frontend run build

test:
	go test ./...
