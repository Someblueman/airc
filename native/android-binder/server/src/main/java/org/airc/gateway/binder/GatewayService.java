package org.airc.gateway.binder;

import android.app.Service;
import android.content.Intent;
import android.os.Binder;
import android.os.IBinder;
import android.os.Parcel;
import android.os.RemoteException;

import java.io.IOException;
import java.util.Map;

/** Exported only through a signature permission; every call also checks the UID and APK pin. */
public final class GatewayService extends Service {
    private static final String CLIENT = "org.airc.gateway.binder.client";
    private static final String PERMISSION = "org.airc.gateway.binder.ACCESS";
    private volatile TlsForwarder forwarder;
    private String clientPin;

    private final Binder endpoint = new Binder() {
        { attachInterface(null, Wire.DESCRIPTOR); }

        @Override protected boolean onTransact(int code, Parcel data, Parcel reply, int flags)
                throws RemoteException {
            if (code == IBinder.INTERFACE_TRANSACTION) return super.onTransact(code, data, reply, flags);
            if (code != Wire.EXCHANGE) return false;
            if (flags != 0 || reply == null) throw new SecurityException("Synchronous exchange required");
            int caller = Binder.getCallingUidOrThrow();
            enforceCallingPermission(PERMISSION, "Gateway signature permission required");
            if (caller != PeerIdentity.requirePackage(GatewayService.this, CLIENT, clientPin)) {
                throw new SecurityException("Unexpected calling UID");
            }
            if (data.dataSize() > Wire.MAX_REQUEST + 1024 || data.hasFileDescriptors()) {
                throw new SecurityException("Invalid Binder frame");
            }
            data.enforceInterface(Wire.DESCRIPTOR);
            int lengthPosition = data.dataPosition();
            int length = data.readInt();
            if (length <= 0 || length > Wire.MAX_REQUEST || length > data.dataAvail()) {
                throw new SecurityException("Invalid Binder request length");
            }
            data.setDataPosition(lengthPosition);
            byte[] request = data.createByteArray();
            if (request == null || request.length != length || data.dataAvail() != 0) {
                throw new SecurityException("Invalid or trailing Binder data");
            }
            TlsForwarder owner = forwarder;
            long identity = Binder.clearCallingIdentity();
            try {
                if (owner == null) throw new IOException("Gateway service is unavailable");
                byte[] response = owner.exchange(request);
                reply.writeNoException();
                reply.writeByteArray(response);
            } catch (IOException | IllegalStateException e) {
                reply.writeException(new IllegalStateException(
                        "Gateway exchange failed or busy; recover the original request if its outcome is uncertain"));
            } finally {
                Binder.restoreCallingIdentity(identity);
            }
            return true;
        }
    };

    @Override public void onCreate() {
        super.onCreate();
        try {
            Map<String, Object> settings = Assets.settings(this, "server.json");
            Wire.requireKeys(settings, "address_ipv4", "port", "server_name", "client_certificate_sha256");
            clientPin = Wire.string(settings, "client_certificate_sha256");
            if (!clientPin.matches("[0-9a-f]{64}")) throw new IOException("Missing client APK pin");
            forwarder = new TlsForwarder(this, settings);
        } catch (IOException | java.security.GeneralSecurityException e) {
            throw new IllegalStateException("Provision valid packaged gateway configuration and CA", e);
        }
    }

    @Override public IBinder onBind(Intent intent) { return endpoint; }

    @Override public void onDestroy() {
        TlsForwarder owner = forwarder;
        forwarder = null;
        if (owner != null) owner.close();
        super.onDestroy();
    }
}
