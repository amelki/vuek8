BINARY=vuek8
VERSION?=0.1.0
APP_NAME=VueK8
LDFLAGS=-s -w -X vuek8/internal/update.Version=$(VERSION)
S3_BUCKET=vuek8-releases
AWS_PROFILE=vuek8
AWS_REGION=eu-west-3
SIGN_IDENTITY=Developer ID Application: Antoine MELKI (9H445KDYMD)
TEAM_ID=9H445KDYMD

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

# Code sign the .app bundle
sign: app
	@echo "Signing $(APP_NAME).app..."
	codesign --deep --force --options runtime \
		--sign "$(SIGN_IDENTITY)" \
		--entitlements build/entitlements.plist \
		dist/$(APP_NAME).app
	@echo "Verifying signature..."
	codesign --verify --deep --strict dist/$(APP_NAME).app
	@echo "Signed."

# macOS .dmg installer with drag-to-Applications
dmg: sign
	rm -rf dist/dmg-staging dist/$(APP_NAME)-$(VERSION).dmg
	mkdir -p dist/dmg-staging
	cp -R dist/$(APP_NAME).app dist/dmg-staging/
	ln -s /Applications dist/dmg-staging/Applications
	hdiutil create -volname "$(APP_NAME)" -srcfolder dist/dmg-staging -ov -format UDZO dist/$(APP_NAME)-$(VERSION).dmg
	rm -rf dist/dmg-staging
	codesign --force --sign "$(SIGN_IDENTITY)" dist/$(APP_NAME)-$(VERSION).dmg
	@echo "Created dist/$(APP_NAME)-$(VERSION).dmg"

# Notarize the DMG with Apple
notarize: dmg
	@echo "Submitting for notarization..."
	xcrun notarytool submit dist/$(APP_NAME)-$(VERSION).dmg \
		--keychain-profile "vuek8-notary" \
		--wait
	@echo "Stapling notarization ticket..."
	xcrun stapler staple dist/$(APP_NAME)-$(VERSION).dmg
	@echo "Notarized."

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
release: binaries notarize
	@mkdir -p dist/release
	cp dist/$(APP_NAME)-$(VERSION).dmg dist/release/$(APP_NAME)-$(VERSION).dmg
	@echo "Uploading to S3..."
	aws --profile $(AWS_PROFILE) --region $(AWS_REGION) s3 cp dist/release/ s3://$(S3_BUCKET)/releases/ \
		--recursive --exclude "*" \
		--include "vuek8-$(VERSION)-*" --include "$(APP_NAME)-$(VERSION).dmg"
	@echo "Uploading latest.json..."
	@echo '{"version":"$(VERSION)","macDmg":"releases/$(APP_NAME)-$(VERSION).dmg","macArm":"releases/vuek8-$(VERSION)-macos-arm64","macIntel":"releases/vuek8-$(VERSION)-macos-amd64","linux":"releases/vuek8-$(VERSION)-linux-amd64","windows":"releases/vuek8-$(VERSION)-windows-amd64.exe"}' \
		| python3 -m json.tool > dist/release/latest.json
	aws --profile $(AWS_PROFILE) --region $(AWS_REGION) s3 cp dist/release/latest.json s3://$(S3_BUCKET)/latest.json \
		--content-type "application/json" --cache-control "max-age=300"
	@echo "Uploading website..."
	aws --profile $(AWS_PROFILE) --region $(AWS_REGION) s3 sync website/ s3://$(S3_BUCKET)/ \
		--exclude ".DS_Store"
	@echo "Invalidating CloudFront cache..."
	aws --profile $(AWS_PROFILE) --region $(AWS_REGION) cloudfront create-invalidation \
		--distribution-id E3Q4IBPDY2EITM --paths "/*" > /dev/null
	@echo "Release $(VERSION) published."

clean:
	rm -rf $(BINARY) dist/
