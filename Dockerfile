FROM golang:1.16.6-stretch

ENV GO111MODULE=on

WORKDIR $GOPATH/src/github.com/pion/ion-sfu

COPY go.mod go.sum ./
RUN cd $GOPATH/src/github.com/pion/ion-sfu && go mod download

COPY sfu/ $GOPATH/src/github.com/pion/ion-sfu/pkg
COPY cmd/ $GOPATH/src/github.com/pion/ion-sfu/cmd
COPY config.toml $GOPATH/src/github.com/pion/ion-sfu/config.toml
