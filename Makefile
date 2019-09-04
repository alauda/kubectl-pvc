.PHONY: all clean

all: build

build:
	CGO_ENABLED=0 GO111MODULE=on GOOS=linux GOARCH=amd64 go build -o _output/kubectl-captain ./cmd/plugin

clean:
	go clean -r -x
	rm _output/kubectl-captain


mod:
	GO111MODULE=on go mod tidy