package org.airc.gateway.binder;

import android.content.Context;
import android.os.SystemClock;

import java.io.ByteArrayInputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.security.GeneralSecurityException;
import java.security.KeyStore;
import java.security.cert.Certificate;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.util.Collection;
import java.util.Collections;
import java.util.Map;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.ScheduledThreadPoolExecutor;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;
import javax.net.ssl.SNIHostName;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLParameters;
import javax.net.ssl.SSLSocket;
import javax.net.ssl.TrustManagerFactory;

/** One fixed upstream, one active socket and no queued submissions or automatic retries. */
final class TlsForwarder implements AutoCloseable {
    private final InetAddress address;
    private final int port;
    private final String serverName;
    private final SSLContext tls;
    private final AtomicReference<Socket> active = new AtomicReference<>();
    private final AtomicBoolean closed = new AtomicBoolean();
    private final ScheduledThreadPoolExecutor timer = new ScheduledThreadPoolExecutor(1, task -> {
        Thread thread = new Thread(task, "airc-binder-deadline");
        thread.setDaemon(true);
        return thread;
    });

    TlsForwarder(Context context, Map<String, Object> settings) throws IOException, GeneralSecurityException {
        address = numericAddress(Wire.string(settings, "address_ipv4"));
        String portText = Wire.string(settings, "port");
        if (!portText.matches("[1-9][0-9]{0,4}")) throw new IOException("Invalid fixed gateway port");
        port = Integer.parseInt(portText);
        if (port > 65535) throw new IOException("Invalid fixed gateway port");
        serverName = Wire.string(settings, "server_name");
        if (serverName.length() > 253 || !serverName.matches("[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?")) {
            throw new IOException("Invalid gateway certificate DNS name");
        }
        // Validate the DNS SNI form before any socket is created.
        new SNIHostName(serverName);
        byte[] pem = Assets.read(context, "gateway-ca.pem", 1 << 20);
        Collection<? extends Certificate> roots = CertificateFactory.getInstance("X.509")
                .generateCertificates(new ByteArrayInputStream(pem));
        if (roots.isEmpty() || roots.size() > 16) throw new IOException("Provision 1 to 16 CA certificates");
        KeyStore store = KeyStore.getInstance(KeyStore.getDefaultType());
        store.load(null, null);
        int index = 0;
        for (Certificate root : roots) {
            if (!(root instanceof X509Certificate) || ((X509Certificate) root).getBasicConstraints() < 0) {
                throw new IOException("Gateway trust bundle must contain CA certificates");
            }
            store.setCertificateEntry("gateway-" + index++, root);
        }
        TrustManagerFactory trust = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
        trust.init(store);
        tls = SSLContext.getInstance("TLS");
        tls.init(null, trust.getTrustManagers(), null);
        timer.setRemoveOnCancelPolicy(true);
    }

    byte[] exchange(byte[] request) throws IOException {
        String correlation = Wire.correlation(request);
        Socket raw = new Socket();
        if (!active.compareAndSet(null, raw)) {
            raw.close();
            throw new IOException("Gateway busy");
        }
        ScheduledFuture<?> deadline = null;
        long started = SystemClock.elapsedRealtime();
        try {
            if (closed.get()) throw new IOException("Gateway stopped");
            deadline = timer.schedule(() -> closeSocket(raw), 30, TimeUnit.SECONDS);
            raw.connect(new InetSocketAddress(address, port), 3000);
            raw.setSoTimeout(3000);
            try (SSLSocket socket = (SSLSocket) tls.getSocketFactory().createSocket(raw, serverName, port, true)) {
                socket.setEnabledProtocols(new String[]{"TLSv1.3"});
                SSLParameters parameters = socket.getSSLParameters();
                parameters.setEndpointIdentificationAlgorithm("HTTPS");
                parameters.setServerNames(Collections.singletonList(new SNIHostName(serverName)));
                parameters.setApplicationProtocols(new String[]{"airc-gateway/1"});
                socket.setSSLParameters(parameters);
                socket.setSoTimeout(3000);
                socket.startHandshake();
                if (!"airc-gateway/1".equals(socket.getApplicationProtocol())) throw new IOException("TLS ALPN mismatch");
                socket.setSoTimeout(30000);
                DataOutputStream output = new DataOutputStream(socket.getOutputStream());
                output.writeInt(request.length);
                output.write(request);
                output.flush();
                DataInputStream input = new DataInputStream(socket.getInputStream());
                int size = input.readInt();
                if (size <= 0 || size > Wire.MAX_RESPONSE) throw new IOException("Gateway response exceeds bound");
                byte[] response = new byte[size];
                input.readFully(response);
                Wire.validateResponse(response, correlation);
                if (closed.get() || SystemClock.elapsedRealtime() - started >= 30000) {
                    throw new IOException("Gateway exchange expired");
                }
                return response;
            }
        } catch (java.util.concurrent.RejectedExecutionException e) {
            throw new IOException("Gateway stopped", e);
        } finally {
            closeSocket(raw);
            if (deadline != null) deadline.cancel(false);
            active.compareAndSet(raw, null);
        }
    }

    @Override public void close() {
        closed.set(true);
        closeSocket(active.get());
        timer.shutdownNow();
        try { timer.awaitTermination(1, TimeUnit.SECONDS); }
        catch (InterruptedException e) { Thread.currentThread().interrupt(); }
    }

    private static void closeSocket(Socket socket) {
        if (socket != null) {
            try { socket.close(); } catch (IOException ignored) { /* Close also used on already failed sockets. */ }
        }
    }

    private static InetAddress numericAddress(String value) throws IOException {
        String[] parts = value.split("\\.", -1);
        if (parts.length != 4) throw new IOException("A fixed numeric IPv4 endpoint is required");
        byte[] bytes = new byte[4];
        for (int i = 0; i < parts.length; i++) {
            if (!parts[i].matches("0|[1-9][0-9]{0,2}")) throw new IOException("Invalid IPv4 component");
            int part = Integer.parseInt(parts[i]);
            if (part > 255) throw new IOException("Invalid IPv4 component");
            bytes[i] = (byte) part;
        }
        if ((bytes[0] & 255) == 0 || (bytes[0] & 255) >= 224) throw new IOException("Unicast gateway required");
        return InetAddress.getByAddress(bytes);
    }
}
