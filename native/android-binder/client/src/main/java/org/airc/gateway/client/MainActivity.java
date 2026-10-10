package org.airc.gateway.client;

import android.app.Activity;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.ServiceConnection;
import android.os.Bundle;
import android.os.IBinder;
import android.os.Handler;
import android.os.Looper;
import android.os.Parcel;
import android.text.InputType;
import android.view.ViewGroup;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.Spinner;
import android.widget.TextView;

import org.airc.gateway.binder.PeerIdentity;
import org.airc.gateway.binder.Assets;
import org.airc.gateway.binder.Wire;

import java.io.IOException;
import java.nio.ByteBuffer;
import java.nio.charset.CharacterCodingException;
import java.nio.charset.CodingErrorAction;
import java.nio.charset.StandardCharsets;
import java.util.UUID;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicReference;
import java.util.regex.Pattern;

public final class MainActivity extends Activity {
    private static final String SERVER_PACKAGE = "org.airc.gateway.binder.server";
    private static final String SERVER_SERVICE = "org.airc.gateway.binder.GatewayService";
    private static final Pattern CORRELATION = Pattern.compile("[A-Za-z0-9_-]{1,64}");
    private static final Pattern SHA256 = Pattern.compile("[0-9a-f]{64}");

    private static final long BINDER_WAIT_MS = 40000;
    private static final AtomicReference<Object> processRequestLease = new AtomicReference<>();
    private static final ExecutorService WORKER = Executors.newSingleThreadExecutor();
    private final Handler handler = new Handler(Looper.getMainLooper());
    private EditText correlationInput;
    private EditText envelopeInput;
    private Spinner profileInput;
    private Button exchangeButton;
    private TextView statusView;
    private TextView resultView;
    private ServiceConnection connection;
    private Object requestLease;
    private boolean bound;
    private boolean workerRunning;
    private boolean timedOut;
    private boolean destroyed;
    private final Runnable deadline = this::onDeadline;

    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        buildForm();
    }

    private void buildForm() {
        int padding = dp(16);
        LinearLayout form = new LinearLayout(this);
        form.setOrientation(LinearLayout.VERTICAL);
        form.setPadding(padding, padding, padding, padding);

        addLabel(form, "Profile");
        profileInput = new Spinner(this);
        profileInput.setAdapter(new ArrayAdapter<>(this, android.R.layout.simple_spinner_dropdown_item,
                new String[]{"signed", "encrypted"}));
        form.addView(profileInput, matchWrap());

        addLabel(form, "Correlation ID");
        correlationInput = new EditText(this);
        correlationInput.setSingleLine(true);
        correlationInput.setFilters(new android.text.InputFilter[]{new android.text.InputFilter.LengthFilter(64)});
        correlationInput.setText(UUID.randomUUID().toString().replace("-", ""));
        form.addView(correlationInput, matchWrap());

        addLabel(form, "Signed or encrypted envelope");
        envelopeInput = new EditText(this);
        envelopeInput.setHint("Paste the compact envelope");
        envelopeInput.setMinLines(4);
        envelopeInput.setMaxLines(8);
        envelopeInput.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_MULTI_LINE |
                InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);
        envelopeInput.setFilters(new android.text.InputFilter[]{new android.text.InputFilter.LengthFilter(Wire.MAX_REQUEST)});
        form.addView(envelopeInput, matchWrap());

        exchangeButton = new Button(this);
        exchangeButton.setText("Exchange");
        exchangeButton.setOnClickListener(view -> beginExchange());
        form.addView(exchangeButton, matchWrap());

        statusView = new TextView(this);
        statusView.setText("Enter an envelope and choose a profile.");
        form.addView(statusView, matchWrap());

        addLabel(form, "Result");
        resultView = new TextView(this);
        resultView.setTextIsSelectable(true);
        resultView.setText("No result yet.");
        form.addView(resultView, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));

        ScrollView scroll = new ScrollView(this);
        scroll.setFitsSystemWindows(true);
        scroll.addView(form);
        setContentView(scroll);
    }

    private void beginExchange() {
        Object ownedLease = new Object();
        if (!processRequestLease.compareAndSet(null, ownedLease)) {
            statusView.setText("A previous Binder call is still active; wait for it to finish.");
            return;
        }
        requestLease = ownedLease;
        String envelope = envelopeInput.getText().toString();
        String correlation = correlationInput.getText().toString();
        String profile = profileInput.getSelectedItem().toString();
        byte[] envelopeBytes = envelope.getBytes(StandardCharsets.UTF_8);
        if (envelope.isEmpty() || envelopeBytes.length > Wire.MAX_REQUEST || !CORRELATION.matcher(correlation).matches()) {
            releaseLease(ownedLease);
            requestLease = null;
            statusView.setText("Provide a bounded envelope and a 1–64 character correlation ID.");
            return;
        }

        final int expectedServerUid;
        try {
            expectedServerUid = PeerIdentity.requirePackage(this, SERVER_PACKAGE, configuredServerDigest());
        } catch (Exception error) {
            releaseLease(ownedLease);
            requestLease = null;
            statusView.setText("Server package identity is not configured or verified.");
            return;
        }
        byte[] request;
        try {
            request = Wire.request(profile, correlation, envelope);
            if (request == null || request.length == 0 || request.length > Wire.MAX_REQUEST) {
                throw new IllegalArgumentException("request exceeds Binder bounds");
            }
        } catch (Exception error) {
            releaseLease(ownedLease);
            requestLease = null;
            statusView.setText("The request is invalid or exceeds the Binder limit.");
            return;
        }

        exchangeButton.setEnabled(false);
        resultView.setText("No result for this exchange yet.");
        statusView.setText("Connecting to the verified gateway…");
        timedOut = false;
        handler.postDelayed(deadline, BINDER_WAIT_MS);
        bindForExchange(request, correlation, expectedServerUid, ownedLease);
    }

    private void bindForExchange(byte[] request, String correlation, int expectedServerUid, Object ownedLease) {
        Intent intent = new Intent().setComponent(new ComponentName(SERVER_PACKAGE, SERVER_SERVICE));
        connection = new ServiceConnection() {
            private boolean dispatched;

            @Override
            public void onServiceConnected(ComponentName name, IBinder service) {
                if (destroyed || connection != this || dispatched || requestLease != ownedLease) return;
                if (timedOut) {
                    finishExchange(ownedLease, "The connection deadline expired before a request was sent.", null);
                    return;
                }
                if (!isExpectedComponent(name)) {
                    finishExchange(ownedLease, "Unexpected service component.", null);
                    return;
                }
                dispatched = true;
                workerRunning = true;
                statusView.setText("Exchanging request…");
                WORKER.execute(() -> {
                    String failure = null;
                    String text = null;
                    try {
                        String descriptor = service.getInterfaceDescriptor();
                        if (!service.isBinderAlive() || !Wire.DESCRIPTOR.equals(descriptor)) {
                            throw new SecurityException("unexpected Binder interface");
                        }
                        String packageName = name.getPackageName();
                        int currentUid = PeerIdentity.requirePackage(getApplicationContext(), packageName,
                                configuredServerDigest());
                        if (currentUid != expectedServerUid) throw new SecurityException("server identity changed");
                        byte[] response = transact(service, request);
                        Wire.validateResponse(response, correlation);
                        text = strictUtf8(response);
                    } catch (Exception error) {
                        failure = "Binder exchange failed or returned an invalid response. Its outcome may be uncertain; recover the original request.";
                    } finally {
                        releaseLease(ownedLease);
                    }
                    final String completedText = text;
                    final String completedFailure = failure;
                    runOnUiThread(() -> {
                        workerRunning = false;
                        if (destroyed || requestLease != ownedLease) return;
                        if (timedOut) {
                            handler.removeCallbacks(deadline);
                            exchangeButton.setEnabled(true);
                            statusView.setText("The Binder call ended after its deadline; its result was suppressed. Outcome is uncertain.");
                            requestLease = null;
                            return;
                        }
                        finishExchange(ownedLease, completedFailure, completedText);
                    });
                });
            }

            @Override
            public void onServiceDisconnected(ComponentName name) {
                if (!destroyed && connection == this && requestLease == ownedLease && !workerRunning) {
                    finishExchange(ownedLease, "Gateway service disconnected.", null);
                }
            }

            @Override
            public void onBindingDied(ComponentName name) {
                if (!destroyed && connection == this && requestLease == ownedLease && !workerRunning) {
                    finishExchange(ownedLease, "Gateway service binding ended.", null);
                }
            }

            @Override
            public void onNullBinding(ComponentName name) {
                if (!destroyed && connection == this && requestLease == ownedLease && !workerRunning) {
                    finishExchange(ownedLease, "Gateway service returned no Binder.", null);
                }
            }
        };
        try {
            bound = bindService(intent, connection, Context.BIND_AUTO_CREATE);
            if (!bound) finishExchange(ownedLease, "Could not bind to the gateway service.", null);
        } catch (RuntimeException error) {
            finishExchange(ownedLease, "Gateway service binding failed.", null);
        }
    }

    private byte[] transact(IBinder service, byte[] request) throws Exception {
        Parcel data = Parcel.obtain();
        Parcel reply = Parcel.obtain();
        try {
            data.writeInterfaceToken(Wire.DESCRIPTOR);
            data.writeByteArray(request);
            if (!service.transact(Wire.EXCHANGE, data, reply, 0)) {
                throw new IllegalStateException("Binder transaction was not handled");
            }
            if (reply.hasFileDescriptors()) throw new SecurityException("Binder reply contains file descriptors");
            if (reply.dataSize() > Wire.MAX_RESPONSE + 1024) {
                throw new IllegalArgumentException("Binder reply Parcel exceeds bound");
            }
            reply.readException();
            int lengthPosition = reply.dataPosition();
            int declaredLength = reply.readInt();
            if (declaredLength <= 0 || declaredLength > Wire.MAX_RESPONSE || declaredLength > reply.dataAvail()) {
                throw new IllegalArgumentException("Binder response length is outside the configured bound");
            }
            reply.setDataPosition(lengthPosition);
            byte[] response = reply.createByteArray();
            if (response == null || response.length != declaredLength || reply.dataAvail() != 0) {
                throw new IllegalArgumentException("invalid Binder response size or trailing data");
            }
            return response;
        } finally {
            reply.recycle();
            data.recycle();
        }
    }

    private String configuredServerDigest() throws Exception {
        Map<String, Object> settings = Assets.settings(getApplicationContext(), "client.json");
        Wire.requireKeys(settings, "server_certificate_sha256");
        Object digest = settings.get("server_certificate_sha256");
        if (!(digest instanceof String) || !SHA256.matcher((String) digest).matches()) {
            throw new IOException("invalid server digest");
        }
        return (String) digest;
    }

    private static String strictUtf8(byte[] value) throws CharacterCodingException {
        return StandardCharsets.UTF_8.newDecoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
                .decode(ByteBuffer.wrap(value)).toString();
    }

    private boolean isExpectedComponent(ComponentName name) {
        return name != null && SERVER_PACKAGE.equals(name.getPackageName()) && SERVER_SERVICE.equals(name.getClassName());
    }

    private void finishExchange(Object ownedLease, String failure, String result) {
        if (destroyed || requestLease != ownedLease) return;
        handler.removeCallbacks(deadline);
        if (bound && connection != null) {
            try {
                unbindService(connection);
            } catch (IllegalArgumentException ignored) {
                // The framework may already have torn down a dead binding.
            }
        }
        bound = false;
        connection = null;
        workerRunning = false;
        releaseLease(ownedLease);
        requestLease = null;
        exchangeButton.setEnabled(true);
        if (failure != null) {
            statusView.setText(failure);
        } else {
            statusView.setText("Exchange complete.");
            resultView.setText(result);
        }
    }

    private void onDeadline() {
        Object ownedLease = requestLease;
        if (ownedLease == null || destroyed || processRequestLease.get() != ownedLease) return;
        timedOut = true;
        if (bound && connection != null) {
            try {
                unbindService(connection);
            } catch (IllegalArgumentException ignored) {
                // The system already ended the binding.
            }
            bound = false;
            connection = null;
        }
        if (workerRunning) {
            statusView.setText("Binder call is still active. Unbinding does not cancel it; the outcome is uncertain.");
        } else {
            releaseLease(ownedLease);
            requestLease = null;
            exchangeButton.setEnabled(true);
            statusView.setText("Connection timed out before a Binder call started; no request was submitted.");
        }
    }

    private void releaseLease(Object ownedLease) {
        if (ownedLease == null) return;
        processRequestLease.compareAndSet(ownedLease, null);
    }

    @Override
    protected void onDestroy() {
        destroyed = true;
        handler.removeCallbacks(deadline);
        if (bound && connection != null) {
            try {
                unbindService(connection);
            } catch (IllegalArgumentException ignored) {
                // Already disconnected.
            }
        }
        if (!workerRunning) {
            releaseLease(requestLease);
            requestLease = null;
        }
        super.onDestroy();
    }

    private void addLabel(LinearLayout parent, String text) {
        TextView label = new TextView(this);
        label.setText(text);
        parent.addView(label, matchWrap());
    }

    private LinearLayout.LayoutParams matchWrap() {
        return new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
    }

    private int dp(int value) {
        return Math.round(value * getResources().getDisplayMetrics().density);
    }
}
