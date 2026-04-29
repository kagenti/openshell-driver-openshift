FROM --platform=$BUILDPLATFORM golang:1.23-bookworm AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /openshell-driver-openshift ./cmd/driver/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /openshell-driver-openshift /openshell-driver-openshift
ENTRYPOINT ["/openshell-driver-openshift"]
