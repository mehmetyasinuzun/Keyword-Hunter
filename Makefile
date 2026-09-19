.PHONY: run build test release docker lint
run:      ; ./run.sh
build:    ; ./run.sh build
test:     ; ./run.sh test
release:  ; ./run.sh release
docker:   ; ./run.sh docker
lint:     ; gofmt -l cmd pkg && go vet ./...
