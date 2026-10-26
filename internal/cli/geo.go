package cli

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// geoBase hosts geoip.dat and geosite.dat, the lists behind geoip: and
// geosite: matches.
const geoBase = "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/"

// useGeoDir points Xray, which otherwise looks next to the binary, at the
// lists geo-update keeps in configDir.
func useGeoDir(configDir string) {
	os.Setenv("XRAY_LOCATION_ASSET", filepath.Join(configDir, "geo"))
}

func (a *app) rulesGeoUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "geo-update",
		Short: "Download geoip.dat and geosite.dat, the lists geoip:/geosite: rules match against",
		Args:  exactArgs(0, "no arguments"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := filepath.Join(a.env.ConfigDir, "geo")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			sizes := map[string]int64{}
			for _, f := range []string{"geoip.dat", "geosite.dat"} {
				n, err := download(cmd, geoBase+f, filepath.Join(dir, f))
				if err != nil {
					return failure("download_failed", fmt.Sprintf("%s: %v", f, err), "the previous file, if any, was kept")
				}
				sizes[f] = n
			}
			return a.ok(map[string]any{"dir": dir, "bytes": sizes}, func(w io.Writer) {
				fmt.Fprintf(w, "updated geoip.dat (%s) and geosite.dat (%s) in %s\n",
					humanBytes(sizes["geoip.dat"]), humanBytes(sizes["geosite.dat"]), dir)
			}, "celare apply")
		},
	}
}

func download(cmd *cobra.Command, url, path string) (int64, error) {
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("server answered %s", resp.Status)
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return 0, err
	}
	return n, os.Rename(tmp, path)
}
