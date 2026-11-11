package feishu

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// VerifySignature 校验飞书回调签名。
//
// @remarks 签名算法：sha256(timestamp + nonce + encryptKey + body)，开启加密推送时必需。
func VerifySignature(encryptKey string, header http.Header, body []byte) bool {
	if encryptKey == "" {
		return true
	}
	timestamp := header.Get("X-Lark-Request-Timestamp")
	nonce := header.Get("X-Lark-Request-Nonce")
	signature := header.Get("X-Lark-Signature")
	if timestamp == "" || nonce == "" || signature == "" {
		return false
	}
	sum := sha256.Sum256([]byte(timestamp + nonce + encryptKey + string(body)))
	return hex.EncodeToString(sum[:]) == signature
}

// DecryptPayload 解密加密推送的事件体。
//
// @remarks 未加密的请求原样返回；密钥为 sha256(encryptKey)。
func DecryptPayload(encryptKey string, body []byte) ([]byte, error) {
	var envelope struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Encrypt == "" {
		return body, nil
	}
	key := sha256.Sum256([]byte(encryptKey))
	raw, err := base64.StdEncoding.DecodeString(envelope.Encrypt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	if len(raw) < aes.BlockSize || len(raw)%aes.BlockSize != 0 {
		return nil, errors.New("密文长度非法")
	}
	iv := raw[:aes.BlockSize]
	plain := make([]byte, len(raw)-aes.BlockSize)
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, raw[aes.BlockSize:])
	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return nil, err
	}
	return plain, nil
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, fmt.Errorf("填充非法")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, fmt.Errorf("填充长度非法")
	}
	if !bytes.Equal(data[len(data)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return nil, fmt.Errorf("填充内容非法")
	}
	return data[:len(data)-pad], nil
}
