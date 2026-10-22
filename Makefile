all: build

# all build for local environment.
.PHONY: build
build:
	mkdir -p bin
	go build -o ./bin/zdxsv ./src/zdxsv
	go build -o ./bin/bench ./src/bench
	go build -o ./bin/zproxy ./src/zproxy

# smoke test of the server binary (scripts/smoke.sh; ports 53 and 443 need cap_net_bind_service).
.PHONY: smoke
smoke:
	scripts/smoke.sh

# execute all tests.
.PHONY: test
test:
	go test -v zdxsv/...

# test with coverage, counting code a test reaches in any package (an e2e test in src/zdxsv
# covers pkg/lobby); per-package report in cover-pkg.txt, per-function in cover.txt, html in cover.html.
.PHONY: cover
cover:
	rm -rf covdata && mkdir covdata
	go test -cover -coverpkg=./... ./... -args -test.gocoverdir=$(CURDIR)/covdata
	go tool covdata percent -i=covdata > cover-pkg.txt
	go tool covdata textfmt -i=covdata -o cover.out
	go tool cover -func=cover.out > cover.txt
	go tool cover -html=cover.out -o cover.html
	cat cover-pkg.txt
	tail -1 cover.txt

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

