# Discord signed-bundle bridge

The additional chat provider is Discord's bot REST API v10. The selected
profile transfers a small signed AIRC bundle in one ordinary guild text-channel
message. `scripts/discord-bundle.py` emits a message or fetches one exact message
from one configured author. The existing `airc-gateway import-bundle` command
authenticates the received intent and performs native admission.

This is an explicit one-way Send bridge. It does not mirror channel history,
follow a live event stream, process arbitrary chat text or publish AIRC
conversation reads back to Discord. IRC, XMPP and Matrix provide the separate
interactive bridge profiles. Discord message receipt is not AIRC acceptance.

## Provisioning and use

Use Linux with Python 3.10+ and keep `gateway_device_io.py` beside the script.
Provision a Discord application bot, its token and the exact numeric bot,
guild, channel and permitted source-author IDs. Use an ordinary guild text
channel (type 0), with the bot's required channel visibility, message-history
and sending permissions. Receiving another account's content may require the
provider's Message Content intent and approval. Empty/redacted content is
refused; the helper does not change permissions, invite a bot or request intents.

The token file must be an absolute canonical private regular file, owned by
root or the current UID, mode 0600 and containing only the raw 20–512-byte
ASCII token, without a trailing newline. Do not put a token in arguments.
The helper verifies the current bot account with `/users/@me`,
then checks the channel's ID, guild and type before reading or sending.
It uses only `https://discord.com/api/v10`, normal certificate/name verification
and a fixed client User-Agent. Redirects, proxy-environment routing, alternate
API hosts and attachment downloads are not part of the profile.

Emit an existing original signed-bundle JSON file:

```sh
python3 scripts/discord-bundle.py emit \
  --token-file /absolute/private/bot-token --bot-id BOT_ID \
  --guild-id GUILD_ID --channel-id CHANNEL_ID \
  --input /absolute/path/signed-bundle.json
```

The successful local JSON report contains the Discord message ID. It reports
provider publication only. Fetch that exact message on the receiving host:

```sh
python3 scripts/discord-bundle.py read \
  --token-file /absolute/private/receiver-bot-token --bot-id RECEIVER_BOT_ID \
  --guild-id GUILD_ID --channel-id CHANNEL_ID \
  --author-id SENDER_BOT_ID --message-id MESSAGE_ID \
  --output /absolute/private/received-bundle.json
```

The same provisioned bot can serve both roles if authorized for that channel.
The reader can also select an allowed human author's message containing the
exact wire form below. Source account IDs are checked as numeric IDs, never
display names. Finally authenticate and import:

```sh
airc-gateway import-bundle --file /absolute/path/gateway.json \
  --input /absolute/private/received-bundle.json
```

Configure exactly one enabled `offline_bundle` listener with explicit native
signing keys, bindings and Send grants, as in [offline-bundle.md](offline-bundle.md).
Discord account selection limits the carrier source. Each signed envelope's
verified native principal determines its AIRC identity and allowed target;
Discord names, bot access and message metadata grant no native authority.
Provision only the intended native principals in the import configuration.

Input is a bounded regular file. Output must be a new mode-0600 file in a
current-user-owned mode-0700 directory. Existing outputs and final-component
symlinks are refused. No source message is deleted, edited or marked consumed.
The script neither signs input nor invokes the importer automatically.

## Wire and delivery bounds

The complete ASCII content is `AIRC_SIGNED_BUNDLE_V1:` followed immediately by
canonical standard padded base64 of 1–1,440 bytes of original bundle JSON.
There is no trailing line ending, embed, attachment, reply or webhook envelope.
The fixed bound keeps the content below Discord's message limit. Base64 is
transport encoding, not encryption: the channel's readers and Discord can see
the signed content. Only the subsequent importer authenticates it.

The reader requires the exact message/channel/author IDs, a default message,
an unedited original timestamp no more than 300 seconds old (with at most five
seconds of positive clock skew), and the exact
bounded wire representation. It refuses other message forms and hidden
content. Native admission separately enforces the envelope's original issued
time and expiry; a new Discord publication cannot renew signed intent.

Emission disables mentions and TTS. Its nonce is the first 24 lowercase hex
characters of SHA-256 over `airc-discord-v1`, a NUL, guild ID, a NUL, channel
ID, a NUL, and the original bundle bytes. It requests `enforce_nonce` and
verifies the returned message's route, bot author and exact content. Discord's
nonce deduplication is short-lived; the native signed request IDs and durable
replay markers remain the delivery recovery boundary.

Each invocation makes only bounded explicit REST calls in a supervised child.
HTTP operations have five-second timeouts, responses are capped at 64 KiB,
and the parent bounds the complete operation to 30 seconds. It terminates,
kills if needed and reaps its child on interruption or timeout. JSON duplicate
keys and nonfinite values are refused. A read publishes the bundle file only
after the child succeeds with the complete payload.

There is no automatic retry, channel scan or rate-limit loop. A timeout or
failure after POST can leave a published Discord message; inspect/recover that
message and reuse the original signed intent. A later duplicate publication
does not grant another native send. Inspect the import report and native
receipt for actual acceptance. Provider authentication failure, missing
permissions, redaction, edits, expiry, size errors and rate limits remain
explicit failed attempts, with no secret or response-body logging.

## Deferred verification

Python source compilation and source review are available. No bot credentials
were read, Discord API calls made, messages sent or native AIRC imports run
during implementation. Actual permission/intent behavior, publication and
exact-message retrieval, rate limits, lost responses, altered payloads,
signature/grant/freshness/replay refusals and joined shutdown remain to verify
with a provisioned isolated channel and an independent native AIRC observer.

Provider contracts: [message resources and nonce behavior](https://docs.discord.com/developers/resources/message),
[channel resources](https://docs.discord.com/developers/resources/channel) and
[versioning, authentication and User-Agent requirements](https://docs.discord.com/developers/reference).
