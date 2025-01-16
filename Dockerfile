FROM golang:1.22 AS build

ENV GOPROXY=https://goproxy.cn,direct
ENV GO111MODULE=on

# 添加构建参数
ARG GIT_COMMIT=unknown
ARG GIT_COMMIT_DATE=unknown
ARG GIT_VERSION=unknown
ARG GIT_TREE_STATE=unknown

WORKDIR /go/cache

# 添加复制 TangSengDaoDaoDaoServerLib 的指令
COPY build_temp/TangSengDaoDaoServerLib ../TangSengDaoDaoServerLib

ADD go.mod .
ADD go.sum .

RUN go mod download

WORKDIR /go/release

ADD . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s -extldflags '-static' \
    -X main.Commit=${GIT_COMMIT} \
    -X main.CommitDate=${GIT_COMMIT_DATE} \
    -X main.Version=${GIT_VERSION} \
    -X main.TreeState=${GIT_TREE_STATE}" \
    -o app ./main.go

FROM alpine as prod
# Import the user and group files from the builder.
COPY --from=build /etc/passwd /etc/passwd
COPY --from=build /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
RUN \ 
    mkdir -p /usr/share/zoneinfo/Asia && \
    mkdir -p /home/tsdddata/logs && \
    ln -s /etc/localtime /usr/share/zoneinfo/Asia/Shanghai
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
WORKDIR /home
COPY --from=build /go/release/app /home
COPY --from=build /go/release/assets /home/assets
COPY --from=build /go/release/configs /home/configs
RUN echo "Asia/Shanghai" > /etc/timezone
ENV TZ=Asia/Shanghai

# 创建日志软链接，将日志输出到stdout
RUN ln -sf /dev/stdout /home/tsdddata/logs/app.log && \
    ln -sf /dev/stderr /home/tsdddata/logs/error.log

# 不加  apk add ca-certificates  apns2推送将请求错误
# RUN  apk add ca-certificates 

ENTRYPOINT ["/home/app"]
