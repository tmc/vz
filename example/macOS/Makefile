.PHONY: all
all: build codesign

.PHONY: dev
dev: build

.PHONY: codesign
codesign:
	codesign --force --entitlements vz.entitlements --deep -s - ./virtualization || true

.PHONY: build
build:
	go build -o virtualization .
