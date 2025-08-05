FROM golang:latest as builder
WORKDIR /build
# disabled for now because i don't wanna switch to buildkit yet :(
# COPY go.mod .
# COPY go.sum .
# RUN go mod download
# COPY . .
# ENV GOCACHE=/root/.cache/go-build
# RUN --mount=type=cache,target="/root/.cache/go-build" go build -o /build/shortrack
COPY . .
ENV CGO_ENABLED=0
RUN go build -o /build/shortrack .

FROM debian:latest
COPY --from=builder /build/shortrack /bin/shortrack
ENTRYPOINT ["/bin/shortrack"]
