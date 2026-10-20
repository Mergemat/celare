# celare builds universal (Apple Silicon + Intel) binaries.
#
#   make test      run every test that doesn't need root
#   make cli       dist/celare
#   make app       dist/Celare.app, with the CLI inside
#   make release   dist/Celare-<version>.zip, dist/celare-<version>-darwin.tar.gz, checksums
#   make run       build the app and open it
#   make site      deploy site/ to celare.omarov.sh

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS := -s -w -X github.com/Mergemat/celare/internal/cli.Version=$(VERSION)
DIST    := dist
APP     := $(DIST)/Celare.app

.PHONY: test cli app release run site clean

test:
	go test ./...
	cd app && swift build

cli:
	@mkdir -p $(DIST)
	GOOS=darwin GOARCH=arm64 go build -ldflags '$(LDFLAGS)' -o $(DIST)/celare-arm64 ./cmd/celare
	GOOS=darwin GOARCH=amd64 go build -ldflags '$(LDFLAGS)' -o $(DIST)/celare-amd64 ./cmd/celare
	lipo -create -output $(DIST)/celare $(DIST)/celare-arm64 $(DIST)/celare-amd64
	rm $(DIST)/celare-arm64 $(DIST)/celare-amd64
	codesign --force --sign - $(DIST)/celare

app: cli
	cd app && swift build -c release --arch arm64 --arch x86_64
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS $(APP)/Contents/Resources
	cp "$$(cd app && swift build -c release --arch arm64 --arch x86_64 --show-bin-path)/Celare" $(APP)/Contents/MacOS/Celare
	cp $(DIST)/celare $(APP)/Contents/Resources/celare
	sed 's/VERSION/$(VERSION)/g' app/Info.plist > $(APP)/Contents/Info.plist
	codesign --force --sign - $(APP)

release: app
	cd $(DIST) && ditto -c -k --keepParent Celare.app Celare-$(VERSION).zip
	cd $(DIST) && tar -czf celare-$(VERSION)-darwin.tar.gz celare
	cd $(DIST) && shasum -a 256 Celare-$(VERSION).zip celare-$(VERSION)-darwin.tar.gz > checksums.txt

run: app
	open $(APP)

site:
	cd site && vercel deploy --prod --yes

clean:
	rm -rf $(DIST) app/.build
