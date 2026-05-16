APP := filesystem
SWAG := swag

.PHONY: build run swagger tidy

build:
	go build ./...

run:
	go run ./cmd --mode master

swagger:
	$(SWAG) init -g cmd/main.go -o docs --parseDependency --parseInternal

tidy:
	go mod tidy
