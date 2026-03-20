BINARY=kglance
VERSION?=0.1.0
APP_NAME=KGlance
LDFLAGS=-s -w -X kglance/internal/update.Version=$(VERSION)

.PHONY: dev build app dmg clean

# Dev mode: fast build, opens in browser (no CGO needed)
dev:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) .
	./$(BINARY) --browser

# Native desktop app
build:
	CC=clang CGO_ENABLED=1 CGO_LDFLAGS="-framework UniformTypeIdentifiers" go build -tags production -ldflags="$(LDFLAGS)" -o $(BINARY) .

# macOS .app bundle
app: build
	rm -rf dist/$(APP_NAME).app
	mkdir -p dist/$(APP_NAME).app/Contents/MacOS
	mkdir -p dist/$(APP_NAME).app/Contents/Resources
	cp $(BINARY) dist/$(APP_NAME).app/Contents/MacOS/$(BINARY)
	cp build/Info.plist dist/$(APP_NAME).app/Contents/

# macOS .dmg installer
dmg: app
	rm -f dist/$(APP_NAME)-$(VERSION).dmg
	hdiutil create -volname "$(APP_NAME)" -srcfolder dist/$(APP_NAME).app -ov -format UDZO dist/$(APP_NAME)-$(VERSION).dmg
	@echo "Created dist/$(APP_NAME)-$(VERSION).dmg"

clean:
	rm -rf $(BINARY) dist/
