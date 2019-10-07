all: build

# all build for comiling check
.PHONY: build
build:
	mkdir -p bin
	go build -o ./bin/zdxsv ./src/zdxsv
	go build -o ./bin/bench ./src/bench
	go build -o ./bin/zproxy ./src/zproxy

.PHONY: router
router:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
		-tags netgo \
		--ldflags '-extldflags "-static"' \
		-o docker/router/router ./src/router
		docker-compose build router

.PHONY: ci
ci:
	mkdir -p bin
	go build -o ./bin/zdxsv ./src/zdxsv
	go build -o ./bin/bench ./src/bench
	go build -o ./bin/zproxy ./src/zproxy

