package filetransfer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"

	"github.com/ntsd/cross-clipboard/pkg/crypto"
	"github.com/ntsd/cross-clipboard/pkg/xerror"
)

// ChunkSize is the default max plaintext bytes per chunk (32 KiB), matching
// zero-share's DEFAULT_SEND_OPTIONS.chunkSize.
const ChunkSize = 32 * 1024

const (
	aesKeyLen = 16 // AES-128, matches zero-share's AES-GCM-128
	ivLen     = 12 // GCM nonce, matches zero-share's encryptAesGcm
)

// GenerateAESKey returns a random AES-128 key.
func GenerateAESKey() ([]byte, error) {
	key := make([]byte, aesKeyLen)
	if _, err := rand.Read(key); err != nil {
		return nil, xerror.NewRuntimeError("generate aes key").Wrap(err)
	}
	return key, nil
}

// EncryptChunk encrypts plaintext with AES-GCM, prepending the 12-byte IV.
// Mirrors zero-share's encryptAesGcm: iv || ciphertext+tag.
func EncryptChunk(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, xerror.NewRuntimeError("new aes cipher").Wrap(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, xerror.NewRuntimeError("new gcm").Wrap(err)
	}
	iv := make([]byte, ivLen)
	if _, err := rand.Read(iv); err != nil {
		return nil, xerror.NewRuntimeError("generate iv").Wrap(err)
	}
	sealed := gcm.Seal(nil, iv, plaintext, nil)
	out := make([]byte, 0, ivLen+len(sealed))
	out = append(out, iv...)
	out = append(out, sealed...)
	return out, nil
}

// DecryptChunk reverses EncryptChunk: splits the 12-byte IV and decrypts.
// Mirrors zero-share's decryptAesGcm.
func DecryptChunk(key, data []byte) ([]byte, error) {
	if len(data) < ivLen {
		return nil, xerror.NewRuntimeErrorf("ciphertext too short: %d", len(data))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, xerror.NewRuntimeError("new aes cipher").Wrap(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, xerror.NewRuntimeError("new gcm").Wrap(err)
	}
	return gcm.Open(nil, data[:ivLen], data[ivLen:], nil)
}

// WrapAESKey encrypts an AES key with the peer's PGP public key. This is the
// cross-clipboard adaptation of zero-share's encryptAesKeyWithRsaPublicKey:
// RSA-OAEP is replaced by the existing per-device OpenPGP encrypter.
func WrapAESKey(encrypter *crypto.PGPEncrypter, aesKey []byte) ([]byte, error) {
	if encrypter == nil {
		return nil, xerror.NewRuntimeError("nil pgp encrypter")
	}
	return encrypter.EncryptMessage(aesKey)
}

// UnwrapAESKey decrypts the PGP-wrapped AES key with the local PGP private key.
// Adaptation of zero-share's decryptAesKeyWithRsaPrivateKey.
func UnwrapAESKey(decrypter *crypto.PGPDecrypter, wrapped []byte) ([]byte, error) {
	if decrypter == nil {
		return nil, xerror.NewRuntimeError("nil pgp decrypter")
	}
	return decrypter.DecryptMessage(wrapped)
}
