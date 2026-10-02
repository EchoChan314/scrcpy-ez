package com.genymobile.scrcpy.device;

import com.genymobile.scrcpy.FakeContext;

import android.content.pm.ApplicationInfo;
import android.content.pm.PackageInfo;
import android.content.pm.PackageManager;
import android.content.res.Configuration;
import android.os.Build;
import android.provider.Settings;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.lang.reflect.Method;
import java.util.List;
import java.util.Map;

/** Metadata only: obtaining this catalog never renders or compresses icons. */
public final class AppCatalog {
    private AppCatalog() { }

    private static String property(String name) {
        try {
            Method get = Class.forName("android.os.SystemProperties").getMethod("get", String.class);
            return (String) get.invoke(null, name);
        } catch (Exception e) {
            return "";
        }
    }

    private static String secureSetting(String name) {
        try {
            String value = Settings.Secure.getString(FakeContext.get().getContentResolver(), name);
            return value == null ? "" : value;
        } catch (RuntimeException e) {
            return "";
        }
    }

    // Shell can inspect Android resource overlays. Older/vendor implementations may omit this service.
    private static JSONArray overlays(PackageManager pm) {
        JSONArray result = new JSONArray();
        try {
            Class<?> sm = Class.forName("android.os.ServiceManager");
            Object binder = sm.getMethod("getService", String.class).invoke(null, "overlay");
            Class<?> stub = Class.forName("android.content.om.IOverlayManager$Stub");
            Object manager = stub.getMethod("asInterface", android.os.IBinder.class).invoke(null, binder);
            Class<?> contract = Class.forName("android.content.om.IOverlayManager");
            Object all = contract.getMethod("getAllOverlays", int.class).invoke(manager, android.os.Process.myUid() / 100000);
            for (Object list : ((Map<?, ?>) all).values()) {
                for (Object overlay : (List<?>) list) {
                    Class<?> type = overlay.getClass();
                    if (!((Boolean) type.getMethod("isEnabled").invoke(overlay))) {
                        continue;
                    }
                    String pkg = (String) type.getField("packageName").get(overlay);
                    PackageInfo p = pm.getPackageInfo(pkg, 0);
                    JSONArray row = new JSONArray();
                    row.put(overlay.toString()).put(p.lastUpdateTime);
                    result.put(row);
                }
            }
        } catch (Exception e) {
            // The standard theme setting and build/locale/density signature remain available.
        }
        return result;
    }

    public static String serial() {
        String value = property("ro.serialno");
        if (value.isEmpty() || "unknown".equalsIgnoreCase(value)) { value = property("ro.boot.serialno"); }
        return value;
    }

    @SuppressWarnings("deprecation")
    public static String build() throws JSONException, PackageManager.NameNotFoundException {
        PackageManager pm = FakeContext.get().getPackageManager();
        JSONObject result = new JSONObject();
        result.put("serial", serial());
        // Avoid volatile orientation/display-size fields, which would dirty every icon on rotation.
        Configuration c = FakeContext.get().getResources().getConfiguration();
        String locale = Build.VERSION.SDK_INT >= 24 ? c.getLocales().toLanguageTags() : c.locale.toLanguageTag();
        result.put("config", Build.FINGERPRINT + "|" + locale + "|" + c.densityDpi + "|"
                + (c.uiMode & Configuration.UI_MODE_NIGHT_MASK) + "|"
                + secureSetting("theme_customization_overlay_packages"));
        JSONArray items = new JSONArray();
        for (ApplicationInfo info : pm.getInstalledApplications(PackageManager.GET_META_DATA)) {
            if (!info.enabled || pm.getLaunchIntentForPackage(info.packageName) == null) {
                continue;
            }
            PackageInfo p;
            try {
                p = pm.getPackageInfo(info.packageName, 0);
            } catch (PackageManager.NameNotFoundException e) {
                continue; // An app may be uninstalled while the catalog is being enumerated.
            }
            long version = Build.VERSION.SDK_INT >= 28 ? p.getLongVersionCode() : p.versionCode;
            JSONObject item = new JSONObject();
            item.put("pkg", info.packageName);
            item.put("name", pm.getApplicationLabel(info).toString());
            item.put("sys", (info.flags & ApplicationInfo.FLAG_SYSTEM) != 0);
            item.put("version", version);
            item.put("updated", p.lastUpdateTime);
            item.put("icon", info.icon);
            items.put(item);
        }
        result.put("resources", overlays(pm));
        result.put("items", items);
        return result.toString();
    }
}
