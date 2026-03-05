.PHONY: build test lint run clean

build:
	go build -o butler ./cmd/butler

test:
	go test -race -cover ./...

lint:
	golangci-lint run ./...

run: build
	./butler -config butler.yaml

clean:
	rm -f butler
