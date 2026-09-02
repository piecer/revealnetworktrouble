package com.checknetwork.app;

import static org.junit.Assert.assertEquals;

import android.app.Application;
import android.content.pm.ApplicationInfo;
import android.content.pm.PackageManager;
import android.content.pm.ProviderInfo;
import android.content.res.XmlResourceParser;

import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;
import org.robolectric.util.ReflectionHelpers;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public class AndroidResourceContractTest {
    @Test
    public void manifestDisablesBackupAndReferencesNetworkSecurityConfig() throws Exception {
        Application application = RuntimeEnvironment.getApplication();
        ApplicationInfo info = application.getPackageManager().getApplicationInfo(
                application.getPackageName(), PackageManager.ApplicationInfoFlags.of(0));

        assertEquals(0, info.flags & ApplicationInfo.FLAG_ALLOW_BACKUP);
        assertEquals(
                R.xml.network_security_config,
                (int) ReflectionHelpers.getField(info, "networkSecurityConfigRes"));
    }

    @Test
    public void networkSecurityResourceIsPackaged() throws Exception {
        try (XmlResourceParser parser = RuntimeEnvironment.getApplication()
                .getResources().getXml(R.xml.network_security_config)) {
            while (parser.next() != XmlResourceParser.START_TAG) {
                // Advance to the document root.
            }
            assertEquals("network-security-config", parser.getName());
        }
    }

    @Test
    public void rawReportProviderIsPrivateGrantOnlyAndUsesOneNarrowCachePath() throws Exception {
        Application application = RuntimeEnvironment.getApplication();
        ProviderInfo provider = null;
        for (ProviderInfo candidate : application.getPackageManager().getPackageInfo(
                application.getPackageName(), PackageManager.PackageInfoFlags.of(
                        PackageManager.GET_PROVIDERS | PackageManager.GET_META_DATA)).providers) {
            if ((application.getPackageName() + ".raw-report-provider").equals(candidate.authority)) {
                provider = candidate;
                break;
            }
        }
        assertEquals(application.getPackageName() + ".raw-report-provider", provider.authority);
        assertEquals(false, provider.exported);
        assertEquals(true, provider.grantUriPermissions);

        int cachePaths = 0;
        try (XmlResourceParser parser = application.getResources().getXml(R.xml.raw_report_paths)) {
            while (parser.next() != XmlResourceParser.END_DOCUMENT) {
                if (parser.getEventType() != XmlResourceParser.START_TAG) continue;
                if ("cache-path".equals(parser.getName())) {
                    cachePaths++;
                    assertEquals("shared_reports", parser.getAttributeValue(null, "name"));
                    assertEquals("shared-reports/", parser.getAttributeValue(null, "path"));
                } else {
                    assertEquals("paths", parser.getName());
                }
            }
        }
        assertEquals(1, cachePaths);
    }
}
