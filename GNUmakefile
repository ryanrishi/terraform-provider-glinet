default: fmt lint install generate

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run

vet32:
	GOOS=linux GOARCH=386 go vet ./...
	GOOS=linux GOARCH=arm go vet ./...

generate:
	cd tools; go generate ./...

fmt:
	gofmt -s -w -e .

test:
	go test -v -cover -timeout=20m -parallel=8 ./...

testacc:
	TF_ACC=1 go test -v -cover -timeout 120m ./...

.PHONY: fmt lint vet32 test testacc build install generate
