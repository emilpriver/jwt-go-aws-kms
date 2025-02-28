package jwtkms

import (
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/golang-jwt/jwt/v4"
)

// RSASigningMethod is an RSA implementation of the SigningMethod interface that uses KMS to Sign/Verify JWTs.
// PS uses the same key as RSA but differ in the algo
type PSSSigningMethod struct {
	RSASigningMethod
	fallbackSigningMethod *jwt.SigningMethodRSAPSS
}

func (m *PSSSigningMethod) Verify(signingString, signature string, keyConfig interface{}) error {
	cfg, ok := keyConfig.(*Config)
	if !ok {
		_, isBuiltInRsa := keyConfig.(*rsa.PublicKey)
		if isBuiltInRsa {
			return m.fallbackSigningMethod.Verify(signingString, signature, keyConfig)
		}

		return jwt.ErrInvalidKeyType
	}

	sig, err := jwt.DecodeSegment(signature)
	if err != nil {
		return fmt.Errorf("decoding signature: %w", err)
	}

	if !m.hash.Available() {
		return jwt.ErrHashUnavailable
	}

	hasher := m.hash.New()
	hasher.Write([]byte(signingString)) //nolint:errcheck
	hashedSigningString := hasher.Sum(nil)

	if cfg.verifyWithKMS {
		return verifyRSAOrPSS(cfg, m.algo, hashedSigningString, sig)
	}

	return localVerifyPSS(cfg, m.hash, hashedSigningString, sig)
}

func localVerifyPSS(cfg *Config, hash crypto.Hash, hashedSigningString []byte, sig []byte) error {
	var rsaPublicKey *rsa.PublicKey

	cachedKey := pubkeyCache.Get(cfg.kmsKeyID)
	if cachedKey == nil {
		getPubKeyOutput, err := cfg.kmsClient.GetPublicKey(cfg.ctx, &kms.GetPublicKeyInput{
			KeyId: aws.String(cfg.kmsKeyID),
		})
		if err != nil {
			return fmt.Errorf("getting public key: %w", err)
		}

		cachedKey, err = x509.ParsePKIXPublicKey(getPubKeyOutput.PublicKey)
		if err != nil {
			return fmt.Errorf("parsing public key: %w", err)
		}

		pubkeyCache.Add(cfg.kmsKeyID, cachedKey)
	}

	rsaPublicKey, ok := cachedKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("invalid key type for key")
	}

	if err := rsa.VerifyPSS(rsaPublicKey, hash, hashedSigningString, sig, &rsa.PSSOptions{}); err != nil {
		return fmt.Errorf("verifying signature locally: %w", err)
	}

	return nil
}
