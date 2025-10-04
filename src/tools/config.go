//go:build release

package tools

//go:embed remote-tools-config.json
var configData []byte

func getConfigData() (data []byte, err error) {
	return configData, nil
}
