package org.airc.gateway.binder;

import android.os.IBinder;
import android.util.JsonReader;
import android.util.JsonToken;
import org.json.JSONObject;

import java.io.IOException;
import java.io.StringReader;
import java.nio.ByteBuffer;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;

/** Versioned, synchronous Binder wrapper for one original framed-v1 request. */
public final class Wire {
    public static final String DESCRIPTOR = "org.airc.gateway.binder.IBoundedGateway.v1";
    public static final int EXCHANGE = IBinder.FIRST_CALL_TRANSACTION;
    public static final int MAX_REQUEST = 16384;
    public static final int MAX_RESPONSE = 65536;

    private Wire() {}

    public static byte[] request(String profile, String correlation, String envelope) throws IOException {
        if (!("signed".equals(profile) || "encrypted".equals(profile)) ||
                correlation == null || !correlation.matches("[A-Za-z0-9_-]{1,64}") ||
                envelope == null || envelope.isEmpty() || envelope.length() > MAX_REQUEST ||
                !envelope.matches("[A-Za-z0-9_.-]+")) {
            throw new IOException("Expected an original compact envelope, profile and correlation");
        }
        try {
            JSONObject wrapper = new JSONObject();
            wrapper.put("version", 1);
            wrapper.put("correlation_id", correlation);
            wrapper.put("profile", profile);
            wrapper.put("envelope", envelope);
            wrapper.put("stream", false);
            byte[] result = wrapper.toString().getBytes(StandardCharsets.UTF_8);
            correlation(result);
            return result;
        } catch (org.json.JSONException e) {
            throw new IOException("Could not encode request", e);
        }
    }

    public static String correlation(byte[] request) throws IOException {
        Map<String, Object> fields = object(request, MAX_REQUEST);
        requireKeys(fields, "version", "correlation_id", "profile", "envelope", "stream");
        String correlation = string(fields, "correlation_id");
        String profile = string(fields, "profile");
        String envelope = string(fields, "envelope");
        if (!(fields.get("version") instanceof NumberToken) ||
                !"1".equals(fields.get("version").toString()) ||
                !Boolean.FALSE.equals(fields.get("stream")) ||
                !correlation.matches("[A-Za-z0-9_-]{1,64}") ||
                !(profile.equals("signed") || profile.equals("encrypted")) ||
                envelope.isEmpty() || !envelope.matches("[A-Za-z0-9_.-]+")) {
            throw new IOException("Invalid nonstreaming framed request");
        }
        return correlation;
    }

    public static void validateResponse(byte[] response, String correlation) throws IOException {
        Map<String, Object> fields = object(response, MAX_RESPONSE);
        String event = string(fields, "event");
        if (!(event.equals("result") || event.equals("error"))) throw new IOException("Nonterminal response");
        requireKeys(fields, "version", "correlation_id", "event", event);
        if (!(fields.get("version") instanceof NumberToken) ||
                !"1".equals(fields.get("version").toString()) ||
                !correlation.equals(string(fields, "correlation_id")) ||
                !(fields.get(event) instanceof Map)) {
            throw new IOException("Invalid or uncorrelated terminal response");
        }
    }

    public static Map<String, Object> object(byte[] data, int maximum) throws IOException {
        if (data == null || data.length == 0 || data.length > maximum) throw new IOException("JSON exceeds bound");
        String text = StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(data)).toString();
        try (JsonReader reader = new JsonReader(new StringReader(text))) {
            reader.setLenient(false);
            Object result = readValue(reader, 0);
            if (!(result instanceof Map) || reader.peek() != JsonToken.END_DOCUMENT) {
                throw new IOException("Expected one JSON object");
            }
            @SuppressWarnings("unchecked") Map<String, Object> object = (Map<String, Object>) result;
            return object;
        } catch (IllegalStateException | NumberFormatException e) {
            throw new IOException("Malformed JSON", e);
        }
    }

    public static String string(Map<String, Object> object, String name) throws IOException {
        Object value = object.get(name);
        if (!(value instanceof String)) throw new IOException("Missing or invalid field: " + name);
        return (String) value;
    }

    public static void requireKeys(Map<String, Object> object, String... keys) throws IOException {
        Set<String> expected = new HashSet<>(Arrays.asList(keys));
        if (!object.keySet().equals(expected)) throw new IOException("Unexpected JSON fields");
    }

    private static Object readValue(JsonReader reader, int depth) throws IOException {
        if (depth > 16) throw new IOException("JSON nesting exceeds bound");
        switch (reader.peek()) {
            case BEGIN_OBJECT:
                Map<String, Object> object = new HashMap<>();
                reader.beginObject();
                while (reader.hasNext()) {
                    String name = reader.nextName();
                    if (object.containsKey(name)) throw new IOException("Duplicate JSON member");
                    object.put(name, readValue(reader, depth + 1));
                }
                reader.endObject();
                return object;
            case BEGIN_ARRAY:
                ArrayList<Object> array = new ArrayList<>();
                reader.beginArray();
                while (reader.hasNext()) array.add(readValue(reader, depth + 1));
                reader.endArray();
                return array;
            case STRING:
                return reader.nextString();
            case NUMBER:
                String number = reader.nextString();
                if (!number.matches("-?(0|[1-9][0-9]*)(\\.[0-9]+)?([eE][+-]?[0-9]+)?")) {
                    throw new IOException("Invalid JSON number");
                }
                return new NumberToken(number);
            case BOOLEAN:
                return reader.nextBoolean();
            case NULL:
                reader.nextNull();
                return null;
            default:
                throw new IOException("Invalid JSON value");
        }
    }

    private static final class NumberToken {
        private final String value;
        NumberToken(String value) { this.value = value; }
        @Override public String toString() { return value; }
    }
}
