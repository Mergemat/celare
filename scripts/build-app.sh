#!/bin/sh
# Builds dist/Celare.app: the SwiftUI menu bar app with the celare CLI
# bundled in Contents/Resources. Signed ad hoc, which is enough to run it
# on this Mac.
set -eu
cd "$(dirname "$0")/.."

VERSION=${VERSION:-$(git describe --always --dirty 2>/dev/null || echo dev)}
APP=dist/Celare.app

go build -ldflags "-X github.com/Mergemat/celare/internal/cli.Version=$VERSION" -o bin/celare ./cmd/celare
(cd app && swift build -c release)

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp app/.build/release/Celare "$APP/Contents/MacOS/Celare"
cp bin/celare "$APP/Contents/Resources/celare"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key><string>sh.celare.app</string>
	<key>CFBundleName</key><string>Celare</string>
	<key>CFBundleExecutable</key><string>Celare</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$VERSION</string>
	<key>CFBundleVersion</key><string>$VERSION</string>
	<key>LSMinimumSystemVersion</key><string>14.0</string>
	<key>LSUIElement</key><true/>
</dict>
</plist>
PLIST

codesign --force --sign - "$APP/Contents/Resources/celare"
codesign --force --sign - "$APP"
echo "built $APP ($VERSION)"
