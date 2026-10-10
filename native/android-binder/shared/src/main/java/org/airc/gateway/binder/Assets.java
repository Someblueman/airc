package org.airc.gateway.binder;

import android.content.Context;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.util.Map;

/** Configuration is packaged into the operator-signed APK, never read from intents. */
public final class Assets {
    private Assets() {}

    public static byte[] read(Context context, String name, int maximum) throws IOException {
        try (InputStream input = context.getAssets().open(name);
             ByteArrayOutputStream output = new ByteArrayOutputStream()) {
            byte[] buffer = new byte[4096];
            int count;
            while ((count = input.read(buffer)) != -1) {
                if (count > maximum - output.size()) throw new IOException("Packaged asset exceeds bound");
                output.write(buffer, 0, count);
            }
            return output.toByteArray();
        }
    }

    public static Map<String, Object> settings(Context context, String name) throws IOException {
        return Wire.object(read(context, name, 4096), 4096);
    }
}
