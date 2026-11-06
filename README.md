# Aquila game server SDK for Go

A fleet server uses it to report Ready, send health pings, learn it was allocated and read its match, and shut down when the match is over.

```sh
go get github.com/aquila-hosting/aquila-hosting-sdk-go
```

```go
client, err := sdk.New()
if err != nil {
	return err
}
if err := client.Ready(ctx); err != nil {
	return err
}
_ = client.Watch(ctx, func(gs *sdk.GameServer) {
	if gs.State == sdk.StateAllocated {
		match := gs.Annotations[sdk.LabelPrefix+"match"]
		// start the match
	}
})
```

`examples/udp-echo` is a complete server.

## Other languages

The SDK is a handful of HTTP calls, described in [`openapi.yaml`](openapi.yaml). The server finds the endpoint in `AQUILA_SDK_URL` and a token in the file named by `AQUILA_SDK_TOKEN_FILE`, which it sends as a bearer token.

A fleet can also speak the Agones SDK protocol, so a game built against one of Agones' SDKs runs on Aquila unchanged. `examples/udp-echo-agones` uses Agones' Go SDK with no Aquila code.

## Mapping to the Agones SDK

| Aquila                        | Agones                                       |
| ----------------------------- | -------------------------------------------- |
| `Ready`                       | `Ready`                                      |
| `Allocate`                    | `Allocate`                                   |
| `Reserve`                     | `Reserve`                                    |
| `Shutdown`                    | `Shutdown`                                   |
| `Health`                      | `Health`                                     |
| `GameServer`                  | `GameServer`                                 |
| `Watch`                       | `WatchGameServer`                            |
| `SetLabel`                    | `SetLabel`                                   |
| `SetAnnotation`               | `SetAnnotation`                              |
| `SetPlayers(count, capacity)` | `Alpha().SetPlayerCapacity`, `PlayerConnect` |
| none                          | Counters and Lists                           |

Labels and annotations set through this SDK are stored under `sdk.aquilahosting.com/`. Through the Agones protocol the same keys read back under `agones.dev/sdk-`.

## License

Apache-2.0. See [LICENSE](LICENSE).
