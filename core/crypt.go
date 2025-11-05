package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

// AES加密函数
func encryptAES(plaintext []byte, key []byte) ([]byte, error) {
	// 生成AES块密码
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// 填充原始数据到块的大小
	blockSize := block.BlockSize()
	padding := blockSize - len(plaintext)%blockSize
	plaintext = append(plaintext, byte(padding)) // 填充最后一个字节

	// 生成一个随机的IV（初始化向量）
	ciphertext := make([]byte, blockSize+len(plaintext))
	iv := ciphertext[:blockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}

	// 创建加密模式
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext[blockSize:], plaintext)

	return ciphertext, nil
}

// AES解密函数
func decryptAES(ciphertext []byte, key []byte) ([]byte, error) {
	// 生成AES块密码
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	// IV是加密数据的前面部分
	blockSize := block.BlockSize()
	if len(ciphertext) < blockSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	iv := ciphertext[:blockSize]
	ciphertext = ciphertext[blockSize:]

	// 创建解密模式
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(ciphertext, ciphertext)

	// 去掉填充
	padding := int(ciphertext[len(ciphertext)-1])
	ciphertext = ciphertext[:len(ciphertext)-padding]

	return ciphertext, nil
}

// 根据密码生成密钥
func generateKey(password string) []byte {
	hash := sha256.New()
	hash.Write([]byte(password))
	return hash.Sum(nil)
}
