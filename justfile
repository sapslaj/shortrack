pb:
  protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    pb/shortrack.proto

build-binary:
  CGO_ENABLED=0 go build -o ./shortrack

build-oci:
  docker build -t oci.sapslaj.xyz/shortrack:latest .

push-binary: build-binary
  scp ./shortrack aqua.sapslaj.xyz:/mnt/exos/volumes/misc/shortrack-binaries/

push-oci: build-oci
  docker push oci.sapslaj.xyz/shortrack:latest
