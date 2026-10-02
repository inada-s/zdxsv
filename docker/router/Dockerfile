FROM golang:alpine as builder
WORKDIR /work
COPY go.mod go.sum ./
RUN go mod download
ADD src/router src/router
RUN CGO_ENABLED=0 go build -tags netgo -o /router ./src/router

FROM scratch
COPY --from=builder /router /router
CMD ["/router"]
