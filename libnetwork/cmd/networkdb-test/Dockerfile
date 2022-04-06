FROM alpine:3.15.4

RUN apk --no-cache add curl

COPY testMain /app/

WORKDIR app

ENTRYPOINT ["/app/testMain"]
