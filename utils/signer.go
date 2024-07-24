package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"
)

// CreateSignature function for creating a signature for url
func CreateSignature(secretKey, data string) string {
	h := hmac.New(sha256.New, []byte(secretKey))
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

// GenerateSignedURL function for generating a signed url
func GenerateSignedURL(secretKey, fileURL string, expiry time.Duration) string {
	expiryTime := time.Now().Add(expiry).Unix()
	dataToSign := fmt.Sprintf("%s\n%d", fileURL, expiryTime)
	signature := CreateSignature(secretKey, dataToSign)

	v := url.Values{}
	v.Set("expires", fmt.Sprintf("%d", expiryTime))
	v.Set("signature", signature)

	signedURL := fmt.Sprintf("%s?%s", fileURL, v.Encode())
	return signedURL
}

// VerifySignature function for verifying the signature of the url
func VerifySignature(secretKey, fileURL, receivedSignature string, expiryTime int64) bool {
	dataToSign := fmt.Sprintf("%s\n%d", fileURL, expiryTime)
	expectedSignature := CreateSignature(secretKey, dataToSign)
	return hmac.Equal([]byte(receivedSignature), []byte(expectedSignature))
}
