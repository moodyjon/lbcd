# lbcd Websockets Example

This example shows how to use the rpcclient package to connect to a btcd RPC
server using TLS-secured websockets, register for block connected and block
disconnected notifications, and get the current block count.

## Running the Example

The first step is to clone the lbcd package:

```bash
$ git clone github.com/lbryio/lbcd
```

Next, navigate to the example's directory and modify the `main.go` source to
specify the correct RPC username and password for the RPC server:

```bash
$ cd rpcclient/examples/lbcdblocknotify
```

```Go
	User: "yourrpcuser",
	Pass: "yourrpcpass",
```

Finally, run it with:

```bash
$ go run .
```

## License

This example is licensed under the [copyfree](http://copyfree.org) ISC License.
