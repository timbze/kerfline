.PHONY: build test install

PREFIX ?= $(HOME)/.local
BIN := kerfline

build:
	go build -o $(BIN) ./cmd/kerfline

test:
	go test ./...

install: build
	install -Dm755 $(BIN) $(PREFIX)/bin/$(BIN)
	install -Dm644 contrib/systemd/kerfline.service $(HOME)/.config/systemd/user/kerfline.service
	systemctl --user daemon-reload
