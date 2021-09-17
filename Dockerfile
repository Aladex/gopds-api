# build stage
FROM golang:1.16 as build-stage
COPY . /app
WORKDIR /app
RUN go mod download && go get -u github.com/go-bindata/go-bindata/... && \
    go-bindata -pkg email -o email/bindata.go -fs -prefix "email/templates" email/templates/... && \
    go build -ldflags "-w -s" -o bin/gopds cmd/*

# production stage
FROM ubuntu:20.04 as production-stage
COPY --from=build-stage /app/bin /gopds
RUN apt update && apt install xz-utils curl -y && \
    curl -L https://github.com/rupor-github/fb2mobi/releases/download/3.6.67/fb2mobi_cli_linux_x86_64_glibc_2.23.tar.xz -o fb2mobi.tar.xz && \
    mkdir /gopds/external_fb2mobi && tar -xf fb2mobi.tar.xz -C /gopds/external_fb2mobi && \
    apt remove curl -y && \
    apt autoremove -y && \
    apt clean
EXPOSE 80
CMD ["/gopds/gopds-api"]