package org.airc.gateway.binder;

import android.content.Context;
import android.content.pm.ApplicationInfo;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.content.pm.Signature;
import android.os.Process;

import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;

/** Pins the current APK signer and rejects shared UIDs and debuggable peers. */
public final class PeerIdentity {
    private PeerIdentity() {}

    public static int requirePackage(Context context, String name, String expected) {
        if (expected == null || !expected.matches("[0-9a-f]{64}")) {
            throw new SecurityException("A provisioned current APK certificate pin is required");
        }
        try {
            PackageManager manager = context.getPackageManager();
            PackageInfo info = manager.getPackageInfo(name, PackageManager.GET_SIGNING_CERTIFICATES);
            ApplicationInfo app = info.applicationInfo;
            if (app == null || !Process.isApplicationUid(app.uid) ||
                    (app.flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0 || info.signingInfo == null) {
                throw new SecurityException("Peer must be a nondebuggable application");
            }
            String[] packages = manager.getPackagesForUid(app.uid);
            // Intentionally pin only the installed APK's current signer, not rotation history.
            Signature[] signers = info.signingInfo.getApkContentsSigners();
            if (packages == null || packages.length != 1 || !name.equals(packages[0]) ||
                    signers == null || signers.length != 1) {
                throw new SecurityException("Shared UIDs and multiple APK signers are not admitted");
            }
            byte[] digest = MessageDigest.getInstance("SHA-256").digest(signers[0].toByteArray());
            byte[] wanted = new byte[32];
            for (int i = 0; i < wanted.length; i++) {
                wanted[i] = (byte) Integer.parseInt(expected.substring(i * 2, i * 2 + 2), 16);
            }
            if (!MessageDigest.isEqual(digest, wanted)) throw new SecurityException("Current APK signer mismatch");
            return app.uid;
        } catch (PackageManager.NameNotFoundException | NoSuchAlgorithmException e) {
            throw new SecurityException("Cannot establish peer application identity", e);
        }
    }
}
