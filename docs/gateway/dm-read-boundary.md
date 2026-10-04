# Direct-message read ownership boundary

DM history, search and watch remain required gateway work. HTTP and the other
bidirectional transports can support them. The current gate is the pinned
upstream identity contract, not a transport or hardware limitation.

The native nickname history selector reads an entire destination inbox. It
does not constrain the sender to the requested conversation peer. An inbox
name alone also cannot identify the recipient account that received a retained
message: native metadata has `from`, `target` and sender `account_id`, but no
recipient account ID. Accounts can be deleted and the nickname registered again
under a different account ID while old inbox messages remain retained.

The real pinned-fixture probe
`TestNativeDirectHistoryCannotIdentifyRecipientAccount` establishes that case:

1. Register a recipient and a sender, and record their public account IDs.
2. Send an accepted DM to the original recipient.
3. Using a fresh fixture-only administrator credential, delete that recipient
   account, close its session and register the same nickname with a new credential.
4. Verify the replacement account ID differs from the original account ID.
5. CHECK the nickname inbox from the replacement session. The original message
   remains readable, with the same destination nickname and the sender's account ID.

The probe runs against immutable revision
`96f16c3bbb8a3faac2e69684f591af63ceb58450`, export SHA-256
`4f25ee66941bd79c29f936b8deabc120dc0451e5d1b747afee4bbdf593b32829`.
It owns its daemon, account/state files, administrator credential and cleanup.
The administrator flag is absent from ordinary gateway fixtures. No live
account, daemon or credential is involved.

```sh
go test -race ./integration/gateway \
  -run '^TestNativeDirectHistoryCannotIdentifyRecipientAccount$' -count=1 -v
```

Filtering `from`/`target` by nicknames would prevent unrelated conversation
bodies from being returned, but it cannot distinguish the two recipient account
generations in this probe. A sender-only account ID does not repair incoming
ownership. Fetching an `@*` audit page and relabeling it as a private conversation
does not supply the missing identity. Existing room reads therefore remain
independent, and the gateway continues to reject direct read targets.

A complete implementation needs a concrete upstream recipient-identity contract
or another equally explicit mapping that establishes both participants for
retained and live records. A recipient account ID must describe identity at
delivery time, survive persistence and account deletion, and remain distinct
from a current nickname lookup. Its support must be negotiated and verified
through the real SDK/wire boundary; legacy records without that fact cannot be
silently attributed to the current recipient. Scope filtering, cursor ownership,
revocation, paging gaps and subscribe/catch-up overlap must then be tested with
unrelated inbox traffic and account replacement.

That mapping is proposed, not implemented or waived. This probe is evidence for
the prerequisite; it is not successful gateway DM-read acceptance, a claim of
private multi-tenancy in AIRC, or a reason to stop other adapter work.
