pb:
  protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    pb/shortrack.proto

scp:
  CGO_ENABLED=0 go build
  ssh 172.24.4.106 sudo systemctl stop shortrack.service
  scp ./shortrack 172.24.4.106:
  ssh 172.24.4.106 sudo cp ./shortrack /usr/local/bin/shortrack
  ssh 172.24.4.106 sudo systemctl start shortrack.service

ecr:
  docker build -t public.ecr.aws/sapslaj/shortrack-testing:latest .
  docker push public.ecr.aws/sapslaj/shortrack-testing:latest

k8s:
  docker build -t homelab-pets-oci-dev-oci.sapslaj.xyz/shortrack:latest .
  docker push homelab-pets-oci-dev-oci.sapslaj.xyz/shortrack:latest
  kubectl rollout restart -n shortrack deployment/shortrack-k8s-provisioner

sigma:
  ssh 172.24.4.106 sudo ./shortrack sigma --volume-dir /srv

play-up:
  ssh 172.24.4.106 sudo ./shortrack play-up

play-down:
  ssh 172.24.4.106 sudo ./shortrack play-down
