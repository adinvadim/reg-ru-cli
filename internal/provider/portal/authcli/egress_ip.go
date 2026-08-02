package authcli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
)

const regRUMyIPURL = "https://www.reg.ru/web-tools/myip/get_data"

const maxMyIPResponseSize = 64 << 10

func resolveREGAPIIPv4(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, regRUMyIPURL, nil)
	if err != nil {
		return "", errors.New("build REG.RU IP discovery request")
	}
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", errors.New("REG.RU IP discovery request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("REG.RU IP discovery was rejected")
	}
	return decodeREGAPIIPv4(response.Body)
}

func decodeREGAPIIPv4(reader io.Reader) (string, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxMyIPResponseSize+1))
	if err != nil || len(body) > maxMyIPResponseSize {
		return "", errors.New("REG.RU IP discovery response is invalid")
	}
	var payload struct {
		IP string `json:"ip"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", errors.New("decode REG.RU IP discovery response")
	}
	address, err := netip.ParseAddr(payload.IP)
	if err != nil || !address.Is4() || !address.IsGlobalUnicast() || address.IsPrivate() {
		return "", errors.New("REG.RU IP discovery did not return a public IPv4 address")
	}
	return address.String(), nil
}
