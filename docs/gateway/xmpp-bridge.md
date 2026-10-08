# XMPP bridge profile

The private `xmppwire` prerequisite parses and encodes standalone XML elements;
it is not connected to authentication, journal admission or the service.
No XMPP mode is qualified by this codec.

The selected codec bounds each element to 64 KiB, nesting to 16 elements,
XML tokens to 2,048 and attributes per element to 32 (including namespace
declarations on input). It requires namespace-qualified elements, compares
expanded names, rejects duplicate expanded attributes, and preserves ordered
mixed content. `Text` refuses child elements so a plain body cannot silently
lose markup. Encoding returns a whole element or no output and bounds its
output buffer, including XML escaping expansion. Cyclic input is refused by
the depth bound. Invalid UTF-8 and XML characters are refused rather than
replaced. XML character references and CDATA retain their decoded content;
XML newline normalization applies on input.

This strict profile refuses DTDs, processing instructions and comments.
It accepts one standalone element, not an unclosed XMPP stream. A future stream
owner must frame bounded children and supply inherited namespace declarations;
stream opening/restart handling is a separate prerequisite. See
[RFC 6120 XML streams](https://www.rfc-editor.org/rfc/rfc6120.html#section-4).

Room and author authentication must be established independently of these
parsed fields. The planned non-anonymous MUC profile will use verified server
membership to map real JIDs, never nicknames alone. See
[XEP-0045](https://xmpp.org/extensions/xep-0045.html).
Replay authority must use the verified room/server's stanza ID and provenance;
client-origin IDs alone are spoofable. See
[XEP-0359](https://xmpp.org/extensions/xep-0359.html#security).
TLS/authentication, membership, loop prevention, durable admission, service
ownership, bounded read replies and real client/server/native qualification
remain in the roadmap.
