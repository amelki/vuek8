BINARY=vuek8
VERSION?=0.1.0
APP_NAME=VueK8
LDFLAGS=-s -w -X vuek8/internal/update.Version=$(VERSION)
S3_BUCKET=vuek8-releases
AWS_PROFILE=vuek8
AWS_REGION=eu-west-3

.PHONY: dev demo build app dmg clean release

# Dev mode: build once, then just refresh browser for frontend changes
# Only re-run 'make dev' when Go code changes
dev:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) .
	./$(BINARY) --browser

# Demo mode: sample data, no real cluster needed
demo:
	go build -ldflags="$(LDFLAGS)" -o $(BINARY) .
	./$(BINARY) --demo --browser

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
	cp build/icon.icns dist/$(APP_NAME).app/Contents/Resources/icon.icns

# macOS .dmg installer
dmg: app
	rm -f dist/$(APP_NAME)-$(VERSION).dmg
	hdiutil create -volname "$(APP_NAME)" -srcfolder dist/$(APP_NAME).app -ov -format UDZO dist/$(APP_NAME)-$(VERSION).dmg
	@echo "Created dist/$(APP_NAME)-$(VERSION).dmg"

# Cross-compile all release binaries
binaries:
	@mkdir -p dist/release
	@echo "Building macOS ARM64..."
	GOOS=darwin GOARCH=arm64 CC=clang CGO_ENABLED=1 CGO_LDFLAGS="-framework UniformTypeIdentifiers" \
		go build -tags production -ldflags="$(LDFLAGS)" -o dist/release/vuek8-$(VERSION)-macos-arm64 .
	@echo "Building macOS AMD64..."
	GOOS=darwin GOARCH=amd64 CC=clang CGO_ENABLED=1 CGO_LDFLAGS="-framework UniformTypeIdentifiers" \
		go build -tags production -ldflags="$(LDFLAGS)" -o dist/release/vuek8-$(VERSION)-macos-amd64 .
	@echo "Building Linux AMD64..."
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		go build -ldflags="$(LDFLAGS)" -o dist/release/vuek8-$(VERSION)-linux-amd64 .
	@echo "Building Windows AMD64..."
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
		go build -ldflags="$(LDFLAGS)" -o dist/release/vuek8-$(VERSION)-windows-amd64.exe .

# Build + upload to S3 + update latest.json
release: binaries
	@echo "Uploading binaries to S3..."
	aws --profile $(AWS_PROFILE) --region $(AWS_REGION) s3 cp dist/release/ s3://$(S3_BUCKET)/releases/ \
		--recursive --exclude "*" \
		--include "vuek8-$(VERSION)-*"
	@echo "Uploading latest.json..."
	@echo '{"version":"$(VERSION)","macArm":"releases/vuek8-$(VERSION)-macos-arm64","macIntel":"releases/vuek8-$(VERSION)-macos-amd64","linux":"releases/vuek8-$(VERSION)-linux-amd64","windows":"releases/vuek8-$(VERSION)-windows-amd64.exe"}' \
		| python3 -m json.tool > dist/release/latest.json
	aws --profile $(AWS_PROFILE) --region $(AWS_REGION) s3 cp dist/release/latest.json s3://$(S3_BUCKET)/latest.json \
		--content-type "application/json" --cache-control "max-age=300"
	@echo "Uploading website..."
	aws --profile $(AWS_PROFILE) --region $(AWS_REGION) s3 sync website/ s3://$(S3_BUCKET)/ \
		--exclude ".DS_Store"
	@echo "Release $(VERSION) published."

clean:
	rm -rf $(BINARY) dist/
