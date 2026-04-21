.PHONY: proto build test clean

BINARY := openshell-driver-openshift
SOCKET := /var/run/openshell-driver.sock

proto:
	buf generate

build:
	go build -o $(BINARY) ./cmd/driver/

test:
	go test ./... -timeout 30s -v

clean:
	rm -f $(BINARY) $(SOCKET)

run: build
	./$(BINARY) --socket $(SOCKET) --namespace openshell-system
