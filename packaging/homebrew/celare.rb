# Rendered into Mergemat/homebrew-tap by .github/workflows/release.yml.
# A formula, not a cask: Homebrew doesn't quarantine formula downloads, so
# the ad-hoc signed app opens without a Gatekeeper prompt.
class Celare < Formula
  desc "Agent-friendly VPN client for every protocol 3x-ui serves"
  homepage "https://github.com/Mergemat/celare"
  url "https://github.com/Mergemat/celare/releases/download/v@VERSION@/Celare-@VERSION@.zip"
  version "@VERSION@"
  sha256 "@SHA256@"

  depends_on :macos

  def install
    # Homebrew unpacks the zip and steps into its one folder, Celare.app.
    (prefix/"Celare.app").install "Contents"
    bin.install_symlink prefix/"Celare.app/Contents/Resources/celare"
  end

  def caveats
    <<~EOS
      Install the background service once (it owns the tunnel, so it needs root),
      and again after each upgrade:
        sudo celare service install

      The menu bar app:
        open #{opt_prefix}/Celare.app
      To find it in Launchpad and Spotlight:
        ln -sf #{opt_prefix}/Celare.app ~/Applications/Celare.app
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/celare version")
  end
end
