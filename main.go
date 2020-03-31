package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/xlzd/gotp"
	"go.mozilla.org/sops/v3/aes"
	"go.mozilla.org/sops/v3/cmd/sops/common"
	"go.mozilla.org/sops/v3/cmd/sops/formats"
	"go.mozilla.org/sops/v3/keyservice"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Accounts []Account
}
type Account struct {
	Name       string
	TOTPSecret string
}

func main() {
	if len(os.Args) != 2 {
		fmt.Println("usage: sotp <account_name>")
		os.Exit(1)
	}
	cfg, err := decryptConfig("config.yaml")
	if err != nil {
		log.Fatal("failed to access configuration at 'config.yaml'", err)
	}
	var totpSecret string
	for _, account := range cfg.Accounts {
		if account.Name == os.Args[1] {
			totpSecret = account.TOTPSecret
			break
		}
	}
	if totpSecret == "" {
		log.Fatal("no totp information found for account", os.Args[1])
	}
	otp := gotp.NewDefaultTOTP(totpSecret)

	fmt.Println("current one-time password is:", otp.Now())
}

func decryptConfig(path string) (cfg Config, err error) {
	// Read the file into an []byte
	encryptedData, err := ioutil.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("failed to read file %q: %w", path, err)
	}

	var svcs []keyservice.KeyServiceClient
	svcs = append(svcs, keyservice.NewLocalClient())
	// try connecting to unix:///tmp/sops.sock
	conn, err := grpc.Dial("unix:///tmp/sops.sock", []grpc.DialOption{grpc.WithInsecure()}...)
	if err == nil {
		// ignore errors but only add the keyservice if the dial call succeded
		svcs = append(svcs, keyservice.NewKeyServiceClient(conn))
	}

	store := common.StoreForFormat(formats.Yaml)

	// Load SOPS file and access the data key
	tree, err := store.LoadEncryptedFile(encryptedData)
	if err != nil {
		return cfg, err
	}
	key, err := tree.Metadata.GetDataKeyWithKeyServices(svcs)
	if err != nil {
		return cfg, err
	}

	// Decrypt the tree
	cipher := aes.NewCipher()
	mac, err := tree.Decrypt(key, cipher)
	if err != nil {
		return cfg, err
	}

	// Compute the hash of the cleartext tree and compare it with
	// the one that was stored in the document. If they match,
	// integrity was preserved
	originalMac, err := cipher.Decrypt(
		tree.Metadata.MessageAuthenticationCode,
		key,
		tree.Metadata.LastModified.Format(time.RFC3339),
	)
	if originalMac != mac {
		return cfg, fmt.Errorf("Failed to verify data integrity. expected mac %q, got %q", originalMac, mac)
	}

	cleartext, err := store.EmitPlainFile(tree.Branches)
	if err != nil {
		return cfg, fmt.Errorf("failed to decrypt file: %w", err)
	}
	err = yaml.Unmarshal(cleartext, &cfg)
	if err != nil {
		return cfg, fmt.Errorf("failed to unmarshal cleartext into yaml: %w", err)
	}
	return
}
