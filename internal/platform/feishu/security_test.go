package feishu_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/moying/agenthub/internal/platform/feishu"
)

func TestVerifySignature(t *testing.T) {
	key := "encrypt-key"
	body := []byte(`{"schema":"2.0"}`)
	header := http.Header{}
	header.Set("X-Lark-Request-Timestamp", "1700000000")
	header.Set("X-Lark-Request-Nonce", "abc")

	if feishu.VerifySignature("", header, body) != true {
		t.Fatal("未配置 EncryptKey 时应跳过校验")
	}
	if feishu.VerifySignature(key, header, body) {
		t.Fatal("缺少签名头应失败")
	}

	sum := sha256.Sum256([]byte("1700000000" + "abc" + key + string(body)))
	header.Set("X-Lark-Signature", hex.EncodeToString(sum[:]))
	if !feishu.VerifySignature(key, header, body) {
		t.Fatal("签名应通过")
	}
	header.Set("X-Lark-Request-Nonce", "other")
	if feishu.VerifySignature(key, header, body) {
		t.Fatal("nonce 变化后应失败")
	}
}

// pad 生成 PKCS7 填充，用于构造加密请求。
func pad(data []byte, blockSize int) []byte {
	n := blockSize - len(data)%blockSize
	return append(data, bytes.Repeat([]byte{byte(n)}, n)...)
}

func TestDecryptPayload(t *testing.T) {
	key := "encrypt-key"
	plain := []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1"}}`)

	block, err := aes.NewCipher(func() []byte { s := sha256.Sum256([]byte(key)); return s[:] }())
	if err != nil {
		t.Fatal(err)
	}
	iv := bytes.Repeat([]byte{7}, aes.BlockSize)
	padded := pad(plain, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	body, _ := json.Marshal(map[string]string{"encrypt": base64.StdEncoding.EncodeToString(append(iv, ciphertext...))})

	got, err := feishu.DecryptPayload(key, body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("解密结果不一致: %s", got)
	}

	// 明文原样返回
	raw := []byte(`{"challenge":"x"}`)
	got, err = feishu.DecryptPayload(key, raw)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("明文应原样返回: %s %v", got, err)
	}
}
