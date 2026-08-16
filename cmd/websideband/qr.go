package main

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/makiuchi-d/gozxing"
	zxingqr "github.com/makiuchi-d/gozxing/qrcode"
	qrcode "github.com/skip2/go-qrcode"
)

const maxQRImageInput = 6 << 20

var addressPattern = regexp.MustCompile(`(?i)(?:lxmf://|lxmf:|rns://|rns:)?([0-9a-f]{32})`)

func (app *application) generateAddressQR(writer http.ResponseWriter, request *http.Request) {
	address := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("address")))
	if !validAddress(address) {
		writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "valid LXMF address is required"})
		return
	}
	png, err := qrcode.Encode(address, qrcode.Medium, 512)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "QR code could not be generated"})
		return
	}
	writer.Header().Set("Content-Type", "image/png")
	writer.Header().Set("Content-Length", strconv.Itoa(len(png)))
	writer.Header().Set("Cache-Control", "private, max-age=3600")
	writer.Header().Set("Content-Disposition", `inline; filename="lxmf-address.png"`)
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(png)
}

func (app *application) decodeAddressQR(writer http.ResponseWriter, request *http.Request) {
	if !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "image/") {
		writeJSON(writer, http.StatusUnsupportedMediaType, map[string]string{"error": "a QR code image is required"})
		return
	}
	payload, ok := readBoundedBody(writer, request, maxQRImageInput)
	if !ok {
		return
	}
	decodedImage, _, err := image.Decode(bytes.NewReader(payload))
	if err != nil {
		writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"error": "image could not be read"})
		return
	}
	bitmap, err := gozxing.NewBinaryBitmapFromImage(decodedImage)
	if err != nil {
		writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"error": "QR code could not be read"})
		return
	}
	result, err := zxingqr.NewQRCodeReader().Decode(bitmap, nil)
	if err != nil {
		writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"error": "no LXMF address QR code found"})
		return
	}
	address := extractAddress(result.GetText())
	if address == "" {
		writeJSON(writer, http.StatusUnprocessableEntity, map[string]string{"error": "QR code does not contain an LXMF address"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"address": address})
}

func extractAddress(value string) string {
	match := addressPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 2 || !validAddress(match[1]) {
		return ""
	}
	return strings.ToLower(match[1])
}
