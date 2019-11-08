all: build

# all build for local environment.
.PHONY: build
build:
	mkdir -p bin
	go build -o ./bin/zdxsv ./src/zdxsv
	go build -o ./bin/bench ./src/bench
	go build -o ./bin/zproxy ./src/zproxy

# build router binary.
.PHONY: router
router:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
		-tags netgo \
		--ldflags '-extldflags "-static"' \
		-o docker/router/router ./src/router
	docker-compose build router

# run go-bindata to pack all assets into a go package.
.PHONY: assets
assets:
	go-bindata -pkg=assets -o=pkg/assets/assets.go ./assets/...

# all build for ci environment.
.PHONY: ci
ci:
	mkdir -p bin
	go build -o ./bin/zdxsv ./src/zdxsv
	go build -o ./bin/bench ./src/bench
	go build -o ./bin/zproxy ./src/zproxy
	go test -v zdxsv/...

