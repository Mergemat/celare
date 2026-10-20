# Rendered into Mergemat/homebrew-tap as Casks/celare.rb by
# .github/workflows/release.yml.
cask "celare" do
  version "@VERSION@"
  sha256 "@SHA256@"

  url "https://github.com/Mergemat/celare/releases/download/v#{version}/Celare-#{version}.zip"
  name "celare"
  desc "Agent-friendly VPN client for VLESS, VMess, Trojan, Shadowsocks, Hysteria2 and WireGuard"
  homepage "https://github.com/Mergemat/celare"

  depends_on macos: :sonoma

  app "Celare.app"
  binary "#{appdir}/Celare.app/Contents/Resources/celare"

  postflight_steps do
    # The app is signed ad hoc, not notarized; Homebrew already checked the
    # download against the sha256 above.
    run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{appdir}}/Celare.app"]
    # Install (or, on upgrade, replace) the background service. If this is
    # skipped, the app offers the same step on first launch.
    run "{{appdir}}/Celare.app/Contents/Resources/celare",
        args:         ["service", "install"],
        sudo:         true,
        must_succeed: false
  end

  uninstall script: {
              executable:   "#{appdir}/Celare.app/Contents/Resources/celare",
              args:         ["service", "uninstall"],
              sudo:         true,
              must_succeed: false,
            },
            quit:   "sh.celare.app"

  zap trash: [
    "/Library/Application Support/celare",
    "/Library/Logs/celare",
    "~/.config/celare",
  ]
end
