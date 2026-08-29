.PHONY: build test install

PREFIX ?= $(HOME)/.local
BIN := telegram-jailbee

build:
	go build -o $(BIN) ./cmd/telegram-jailbee

test:
	go test ./...

install: build
	install -Dm755 $(BIN) $(PREFIX)/bin/$(BIN)
	install -Dm644 contrib/systemd/telegram-jailbee.service $(HOME)/.config/systemd/user/telegram-jailbee.service
	systemctl --user daemon-reload
