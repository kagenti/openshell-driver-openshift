.PHONY: proto build test test-unit test-grpc test-integration test-all clean run

BINARY := openshell-driver-openshift
SOCKET := /var/run/openshell-driver.sock

proto:
	buf generate
	mkdir -p gen/computev1
	mv gen/compute_driver*.go gen/computev1/ 2>/dev/null || true

build:
	go build -o $(BINARY) ./cmd/driver/

test-unit:
	go test ./internal/driver/ -timeout 30s -v

test-grpc:
	go test ./internal/grpctest/ -timeout 30s -v

test-integration:
	go test ./test/integration/ -tags integration -timeout 120s -v

test: test-unit test-grpc

test-all: test test-integration

clean:
	rm -f $(BINARY) $(SOCKET)

run: build
	./$(BINARY) --socket $(SOCKET)
