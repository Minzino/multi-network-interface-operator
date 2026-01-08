package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
)

// DecryptAESCBC decrypts base64(IV + ciphertext) with AES-128-CBC (PKCS7).
func DecryptAESCBC(b64Cipher, key string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64Cipher)
	if err != nil {
		return "", err
	}
	if len(raw) < aes.BlockSize {
		return "", errors.New("ciphertext too short")
	}
	iv, ct := raw[:aes.BlockSize], raw[aes.BlockSize:]

	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", err
	}
	if len(ct)%aes.BlockSize != 0 {
		return "", errors.New("ciphertext is not a multiple of block size")
	}
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(ct, ct)

	plain, err := pkcs7Unpad(ct, aes.BlockSize)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("invalid padding size")
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, errors.New("invalid padding")
	}
	for i := len(data) - pad; i < len(data); i++ {
		if data[i] != byte(pad) {
			return nil, errors.New("invalid padding content")
		}
	}
	return data[:len(data)-pad], nil
}
