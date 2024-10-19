FROM golang:1.23 as build

WORKDIR /go/src/controller
COPY . .

RUN go mod download
RUN go vet -v
RUN go test -v

RUN CGO_ENABLED=0 go build -o /go/bin/controller

FROM gcr.io/distroless/static-debian12

COPY --from=build /go/bin/controller /
CMD ["/controller"]
