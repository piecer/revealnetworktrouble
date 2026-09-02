package com.checknetwork.app;

import static org.junit.Assert.assertFalse;
import static org.junit.Assert.assertEquals;

import android.content.res.XmlResourceParser;

import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public class ReleaseNetworkSecurityContractTest {
    @Test public void releaseDefaultRequiresExplicitHttpsOrigin() {
        assertEquals("", RuntimeEnvironment.getApplication().getString(R.string.default_api_base));
    }
    @Test
    public void releaseResourcesRejectCleartextTraffic() throws Exception {
        try (XmlResourceParser parser = RuntimeEnvironment.getApplication()
                .getResources().getXml(R.xml.network_security_config)) {
            while (parser.next() != XmlResourceParser.END_DOCUMENT) {
                if (parser.getEventType() == XmlResourceParser.START_TAG
                        && "base-config".equals(parser.getName())) {
                    assertFalse(parser.getAttributeBooleanValue(
                            null, "cleartextTrafficPermitted", true));
                    return;
                }
            }
        }

        throw new AssertionError("network security config has no base-config element");
    }
}
