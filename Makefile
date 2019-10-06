all: build

.PHONY: download
download:
	go mod download

# all build for comiling check
.PHONY: build
build:
	mkdir -p bin
	go build -o ./bin/zdxsv ./src/zdxsv
	go build -o ./bin/bench ./src/bench
	go build -o ./bin/zproxy ./src/zproxy

# build zdxsv for docker execution
.PHONY: docker
docker:
	mkdir -p docker/zdxsv
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
		-tags netgo \
		--ldflags '-extldflags "-static"' \
		-o docker/zdxsv/zdxsv ./src/zdxsv

	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
		-tags netgo \
		--ldflags '-extldflags "-static"' \
		-o docker/tlsrouter/tlsrouter ./src/tlsrouter
	docker-compose build tlsrouter

.PHONY: ci
ci:
	mkdir -p bin
	go build -o ./bin/zdxsv ./src/zdxsv
	go build -o ./bin/bench ./src/bench
	go build -o ./bin/zproxy ./src/zproxy

