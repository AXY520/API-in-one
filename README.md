# API-in-one

## Update Channel Keys

Read existing upstream keys for one channel:

```bash
curl 'http://HOST:3000/admin/channels/CHANNEL_NAME/keys' \
  -H 'Authorization: Bearer ADMIN_KEY'
```

Replace upstream keys for one channel without changing other channel settings:

```bash
curl -X PUT 'http://HOST:3000/admin/channels/CHANNEL_NAME/keys' \
  -H 'Authorization: Bearer ADMIN_KEY' \
  -H 'Content-Type: application/json' \
  -d '{"keys":["sk-key-1","sk-key-2"]}'
```

`keys` can also be a newline/comma/semicolon separated string:

```json
{"keys":"sk-key-1\nsk-key-2"}
```
