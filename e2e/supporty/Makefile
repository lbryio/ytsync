BINARY=supporty

DIR = $(shell cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd)
BIN_DIR = ${DIR}

.PHONY: build clean test lint
.DEFAULT_GOAL: build


build:
	mkdir -p ${BIN_DIR} && CGO_ENABLED=0 go build -asmflags -trimpath=${DIR} -o ${BIN_DIR}/${BINARY} supporty.go
	chmod +x ${BIN_DIR}/${BINARY}