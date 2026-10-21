# celare builds universal (Apple Silicon + Intel) binaries.
#
#   make test      run every test that doesn't need root
#   make cli       dist/celare
#   make app       dist/Celare.app, with the CLI inside
#   make release   dist/Celare-<version>.zip, dist/celare-<version>-darwin.tar.gz, checksums
#   make run       build the app and open it
#   make icon      redraw app/AppIcon.icns from the mark in app/Sources/Celare/Mark.swift
#
# site/ deploys to celare.omarov.sh on every push to main, through Vercel's
# GitHub integration.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS := -s -w -X github.com/Mergemat/celare/internal/cli.Version=$(VERSION)
DIST    := dist
APP     := $(DIST)/Celare.app

.PHONY: test cli app release run icon clean

test:
	go test ./...
	cd app && swift test

cli:
	@mkdir -p $(DIST)
	CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 go build -ldflags '$(LDFLAGS)' -o $(DIST)/celare-arm64 ./cmd/celare
	CGO_ENABLED=1 GOOS=darwin GOARCH=amd64 go build -ldflags '$(LDFLAGS)' -o $(DIST)/celare-amd64 ./cmd/celare
	lipo -create -output $(DIST)/celare $(DIST)/celare-arm64 $(DIST)/celare-amd64
	rm $(DIST)/celare-arm64 $(DIST)/celare-amd64
	codesign --force --sign - $(DIST)/celare

app: cli
	cd app && swift build -c release --arch arm64 --arch x86_64
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources
	cp "$$(cd app && swift build -c release --arch arm64 --arch x86_64 --show-bin-path)/Celare" $(APP)/Contents/MacOS/Celare
	# SwiftPM stamps the deployment target as the SDK version, and macOS
	# then draws the app in its old look (smaller corners, old controls).
	vtool -set-build-version macos 14.0 $$(xcrun --sdk macosx --show-sdk-version) -replace \
		-output $(APP)/Contents/MacOS/Celare $(APP)/Contents/MacOS/Celare
	cp $(DIST)/celare $(APP)/Contents/Resources/celare
	cp app/AppIcon.icns $(APP)/Contents/Resources/AppIcon.icns
	sed 's/VERSION/$(VERSION)/g' app/Info.plist > $(APP)/Contents/Info.plist
	codesign --force --sign - $(APP)

release: app
	cd $(DIST) && ditto -c -k --keepParent Celare.app Celare-$(VERSION).zip
	cd $(DIST) && tar -czf celare-$(VERSION)-darwin.tar.gz celare
	cd $(DIST) && shasum -a 256 Celare-$(VERSION).zip celare-$(VERSION)-darwin.tar.gz > checksums.txt

run: app
	open $(APP)

icon:
	cd app && swift build -c release
	rm -rf $(DIST)/AppIcon.iconset && mkdir -p $(DIST)/AppIcon.iconset
	app/.build/release/Celare --render-icon $(DIST)/icon-1024.png
	for size in 16 32 128 256 512; do \
		sips -z $$size $$size $(DIST)/icon-1024.png --out $(DIST)/AppIcon.iconset/icon_$${size}x$${size}.png >/dev/null; \
		double=$$((size * 2)); \
		sips -z $$double $$double $(DIST)/icon-1024.png --out $(DIST)/AppIcon.iconset/icon_$${size}x$${size}@2x.png >/dev/null; \
	done
	iconutil -c icns $(DIST)/AppIcon.iconset -o app/AppIcon.icns

clean:
	rm -rf $(DIST) app/.build
