package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"dagger.io/dagger"
	"github.com/1Password/connect-sdk-go/connect"
	"github.com/1Password/connect-sdk-go/onepassword"
	"github.com/hashicorp/vault-client-go"
	"github.com/hashicorp/vault-client-go/schema"
)

type InitializeResponse struct {
	Keys       []string `json:"keys"`
	KeysBase64 []string `json:"keys_base64"`
	RootToken  string   `json:"root_token"`
}

func main() {
	ctx := context.Background()
	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	opCache := client.CacheVolume("op-data")
	credentials := client.SetSecret("op-credentials", os.Getenv("OP_CREDENTIALS_JSON"))

	opConnectSync := client.Container().
		From("1password/connect-sync:latest").
		WithExposedPort(8080).
		WithMountedSecret("/home/opuser/.op/1password-credentials.json", credentials).
		WithMountedCache("/home/opuser/.op/data", opCache, dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.Shared,
			Owner:   "opuser:opuser",
		})

	opConnectSyncSvc, err := opConnectSync.AsService().Start(ctx)

	opConnectApi := client.Container().
		From("1password/connect-api:latest").
		WithExposedPort(8080).
		WithMountedSecret("/home/opuser/.op/1password-credentials.json", credentials).
		WithMountedCache("/home/opuser/.op/data", opCache, dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.Shared,
			Owner:   "opuser:opuser",
		})
	opConnectApiSvc := opConnectApi.AsService()
	if err != nil {
		panic(err)
	}
	opConnectApi.WithServiceBinding("op-connect-sync", opConnectSyncSvc)

	tunnel, err := client.Host().Tunnel(opConnectApiSvc).Start(ctx)
	if err != nil {
		panic(err)
	}
	defer tunnel.Stop(ctx)

	srvAddr, err := tunnel.Endpoint(ctx)
	if err != nil {
		panic(err)
	}

	opClient := connect.NewClient(fmt.Sprintf("http://%s", srvAddr), os.Getenv("OP_CONNECT_TOKEN"))
	opVault, err := opClient.GetVaultByTitle(os.Getenv("OP_VAULT_NAME"))
	if err != nil {
		panic(err)
	}
	vaultClient, err := vault.New(
		vault.WithAddress(os.Getenv("VAULT_ADDR")),
		vault.WithRequestTimeout(30*time.Second),
	)
	if err != nil {
		panic(err)
	}

	sealStatusResponse, err := vaultClient.System.SealStatus(ctx)

	if err != nil {
		panic(err)
	}

	intent := os.Args[1]
	if intent == "init" && !sealStatusResponse.Data.Initialized {
		response, err := vaultClient.System.Initialize(ctx, schema.InitializeRequest{
			SecretThreshold: 3,
			SecretShares:    5,
		})
		if err != nil {
			panic(err)
		}
		payload, err := json.Marshal(response.Data)
		if err != nil {
			panic(err)
		}
		result := InitializeResponse{}
		json.Unmarshal(payload, &result)
		fields := []*onepassword.ItemField{{
			Value: result.RootToken,
			Type:  onepassword.FieldTypeConcealed,
			Label: "Root Token",
		}}
		for i, key := range result.KeysBase64 {
			fields = append(fields, &onepassword.ItemField{
				Value: key,
				Type:  onepassword.FieldTypeConcealed,
				Label: fmt.Sprintf("%s %d", os.Getenv("OP_UNSEAL_KEY_FIELD_NAME"), i),
			})
		}
		item := &onepassword.Item{
			Title:    "Vault",
			Category: onepassword.Custom,
			Fields:   fields,
		}
		opClient.CreateItem(item, opVault.ID)
	} else if intent == "unseal" && sealStatusResponse.Data.Sealed {
		item, err := opClient.GetItemByTitle("Vault", opVault.ID)
		if err != nil {
			panic(err)
		}
		for i := range sealStatusResponse.Data.T {
			fieldName := fmt.Sprintf("%s %d", os.Getenv("OP_UNSEAL_KEY_FIELD_NAME"), i)
			unsealKey := item.GetValue(fieldName)
			if len(unsealKey) > 0 {
				vaultClient.System.Unseal(ctx, schema.UnsealRequest{
					Key: unsealKey,
				})
			}
		}
	}
	postOpSealStatus, err := vaultClient.System.SealStatus(ctx)

	if err != nil {
		panic(err)
	}

	fmt.Println(postOpSealStatus.Data)
}
