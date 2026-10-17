package com.genymobile.scrcpy.wrappers;

import com.genymobile.scrcpy.AndroidVersions;
import com.genymobile.scrcpy.FakeContext;
import com.genymobile.scrcpy.util.Ln;

import android.os.Build;
import android.os.Binder;
import android.os.IBinder;
import android.os.IInterface;
import android.os.WorkSource;

import java.lang.reflect.Method;

public final class PowerManager {

    private static final int SCREEN_BRIGHT_WAKE_LOCK = 10;

    private final IInterface manager;
    private Method isScreenOnMethod;
    private Method acquireWakeLockMethod;
    private Method releaseWakeLockMethod;

    static PowerManager create() {
        IInterface manager = ServiceManager.getService("power", "android.os.IPowerManager");
        return new PowerManager(manager);
    }

    private PowerManager(IInterface manager) {
        this.manager = manager;
    }

    private Method getIsScreenOnMethod() throws NoSuchMethodException {
        if (isScreenOnMethod == null) {
            if (Build.VERSION.SDK_INT >= AndroidVersions.API_34_ANDROID_14) {
                isScreenOnMethod = manager.getClass().getMethod("isDisplayInteractive", int.class);
            } else {
                isScreenOnMethod = manager.getClass().getMethod("isInteractive");
            }
        }
        return isScreenOnMethod;
    }

    public boolean isScreenOn(int displayId) {

        try {
            Method method = getIsScreenOnMethod();
            if (Build.VERSION.SDK_INT >= AndroidVersions.API_34_ANDROID_14) {
                return (boolean) method.invoke(manager, displayId);
            }
            return (boolean) method.invoke(manager);
        } catch (ReflectiveOperationException e) {
            Ln.e("Could not invoke method", e);
            return false;
        }
    }

    private Method getAcquireWakeLockMethod() throws ReflectiveOperationException {
        if (acquireWakeLockMethod == null) {
            Class<?> type = manager.getClass();
            try {
                acquireWakeLockMethod = type.getMethod("acquireWakeLock", IBinder.class, int.class, String.class, String.class,
                        WorkSource.class, String.class, int.class, Class.forName("android.os.IWakeLockCallback"));
            } catch (ClassNotFoundException | NoSuchMethodException e) {
                try {
                    acquireWakeLockMethod = type.getMethod("acquireWakeLock", IBinder.class, int.class, String.class, String.class,
                            WorkSource.class, String.class, int.class);
                } catch (NoSuchMethodException e2) {
                    acquireWakeLockMethod = type.getMethod("acquireWakeLock", IBinder.class, int.class, String.class, String.class,
                            WorkSource.class, String.class);
                }
            }
        }
        return acquireWakeLockMethod;
    }

    public IBinder acquireScreenWakeLock(int displayId) throws ReflectiveOperationException {
        Method method = getAcquireWakeLockMethod();
        int count = method.getParameterTypes().length;
        if (count == 6 && displayId != 0) {
            throw new UnsupportedOperationException("Display-scoped screen wake locks are unavailable");
        }
        IBinder token = new Binder();
        try {
            // No ACQUIRE_CAUSES_WAKEUP or ON_AFTER_RELEASE: never simulate user activity,
            // relight a manually locked screen, or reset its idle timer when releasing.
            if (count == 8) {
                method.invoke(manager, token, SCREEN_BRIGHT_WAKE_LOCK, "scrcpy-ez:screen-awake", FakeContext.PACKAGE_NAME,
                        null, null, displayId, null);
            } else if (count == 7) {
                method.invoke(manager, token, SCREEN_BRIGHT_WAKE_LOCK, "scrcpy-ez:screen-awake", FakeContext.PACKAGE_NAME,
                        null, null, displayId);
            } else {
                method.invoke(manager, token, SCREEN_BRIGHT_WAKE_LOCK, "scrcpy-ez:screen-awake", FakeContext.PACKAGE_NAME,
                        null, null);
            }
            Ln.d("Screen wake lock acquired (display " + displayId + ")");
            return token;
        } catch (ReflectiveOperationException e) {
            // A failed reply does not prove that the service did not acquire the token.
            try {
                releaseScreenWakeLock(token);
            } catch (ReflectiveOperationException cleanupError) {
                e.addSuppressed(cleanupError);
            }
            throw e;
        }
    }

    public void releaseScreenWakeLock(IBinder token) throws ReflectiveOperationException {
        if (releaseWakeLockMethod == null) {
            releaseWakeLockMethod = manager.getClass().getMethod("releaseWakeLock", IBinder.class, int.class);
        }
        releaseWakeLockMethod.invoke(manager, token, 0);
        Ln.d("Screen wake lock released");
    }
}
