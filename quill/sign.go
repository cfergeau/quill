package quill

import (
	"fmt"
	"path"

	"github.com/anchore/quill/internal/log"
	"github.com/anchore/quill/quill/macho"
	"github.com/anchore/quill/quill/pem"
	"github.com/anchore/quill/quill/sign"
)

type SigningConfig struct {
	SigningMaterial pem.SigningMaterial
	Identity        string
	Path            string
}

func NewEmptySigningConfig(binaryPath string) (*SigningConfig, error) {
	return &SigningConfig{
		Path:     binaryPath,
		Identity: path.Base(binaryPath),
	}, nil
}

func NewSigningConfigFromPEMs(binaryPath, certificate, privateKey, password string) (*SigningConfig, error) {
	var signingMaterial pem.SigningMaterial
	if certificate != "" {
		sm, err := pem.NewSigningMaterialFromPEMs(certificate, privateKey, password)
		if err != nil {
			return nil, err
		}

		if err := validateCertificateMaterial(sm); err != nil {
			return nil, err
		}
		signingMaterial = *sm
	}

	return &SigningConfig{
		Path:            binaryPath,
		Identity:        path.Base(binaryPath),
		SigningMaterial: signingMaterial,
	}, nil
}

func NewSigningConfigFromP12(binaryPath, p12, password string) (*SigningConfig, error) {
	signingMaterial, err := pem.NewSigningMaterialFromP12(p12, password)
	if err != nil {
		return nil, err
	}

	if err := validateCertificateMaterial(signingMaterial); err != nil {
		return nil, err
	}

	return &SigningConfig{
		Path:            binaryPath,
		Identity:        path.Base(binaryPath),
		SigningMaterial: *signingMaterial,
	}, nil
}

func (c *SigningConfig) WithIdentity(id string) *SigningConfig {
	if id != "" {
		c.Identity = id
	}
	return c
}

func (c *SigningConfig) WithTimestampServer(url string) *SigningConfig {
	c.SigningMaterial.TimestampServer = url
	return c
}

func Sign(cfg SigningConfig) error {
	log.WithFields("binary", cfg.Path).Info("signing binary")

	m, err := macho.NewFile(cfg.Path)
	if err != nil {
		return err
	}

	// check there already isn't a LcCodeSignature loader already (if there is, bail)
	if m.HasCodeSigningCmd() {
		log.Debug("binary already signed, removing signature...")
		if err := m.RemoveSigningContent(); err != nil {
			return fmt.Errorf("unable to remove existing code signature: %+v", err)
		}
	}

	if cfg.SigningMaterial.Signer == nil {
		log.Warnf("only ad-hoc signing, which means that anyone can alter the binary contents without you knowing (there is no cryptographic signature)")
	}

	// (patch) add empty LcCodeSignature loader (offset and size references are not set)
	if err = m.AddEmptyCodeSigningCmd(); err != nil {
		return err
	}

	// first pass: add the signed data with the dummy loader
	log.Debugf("estimating signing material size")
	superBlobSize, sbBytes, err := sign.GenerateSigningSuperBlob(cfg.Identity, m, cfg.SigningMaterial, 0)
	if err != nil {
		return fmt.Errorf("failed to add signing data on pass=1: %w", err)
	}

	// (patch) make certain offset and size references to the superblob are finalized in the binary
	log.Debugf("patching binary with updated superblob offsets")
	if err = sign.UpdateSuperBlobOffsetReferences(m, uint64(len(sbBytes))); err != nil {
		return nil
	}

	// second pass: now that all of the sizing is right, let's do it again with the final contents (replacing the hashes and signature)
	log.Debug("creating signature for binary")
	_, sbBytes, err = sign.GenerateSigningSuperBlob(cfg.Identity, m, cfg.SigningMaterial, superBlobSize)
	if err != nil {
		return fmt.Errorf("failed to add signing data on pass=2: %w", err)
	}

	// (patch) append the superblob to the __LINKEDIT section
	log.Debugf("patching binary with signature")

	codeSigningCmd, _, err := m.CodeSigningCmd()
	if err != nil {
		return err
	}

	if err = m.Patch(sbBytes, len(sbBytes), uint64(codeSigningCmd.DataOffset)); err != nil {
		return fmt.Errorf("failed to patch super blob onto macho binary: %w", err)
	}

	return nil
}

func validateCertificateMaterial(signingMaterial *pem.SigningMaterial) error {
	// verify chainArgs of trust is already done on load
	// if _, err := certificate.Load(appConfig.Sign.Certificates); err != nil {
	//	return err
	//}

	// verify leaf has x509 code signing extensions

	// verify remaining requirements from  https://images.apple.com/certificateauthority/pdf/Apple_Developer_ID_CPS_v3.3.pdf
	return nil
}