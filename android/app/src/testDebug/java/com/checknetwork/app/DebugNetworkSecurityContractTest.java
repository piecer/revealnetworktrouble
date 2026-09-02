package com.checknetwork.app;

import static org.junit.Assert.assertTrue;
import static org.junit.Assert.assertEquals;

import android.content.res.XmlResourceParser;

import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 35)
public class DebugNetworkSecurityContractTest {
    @Test public void debugDefaultUsesEmulatorLoopbackHttp() {
        assertEquals("http://10.0.2.2:8080", RuntimeEnvironment.getApplication().getString(R.string.default_api_base));
    }
    @Test
    public void debugResourcesPermitLocalCleartextDevelopmentTraffic() throws Exception {
        try (XmlResourceParser parser = RuntimeEnvironment.getApplication()
                .getResources().getXml(R.xml.network_security_config)) {
            while (parser.next() != XmlResourceParser.END_DOCUMENT) {
                if (parser.getEventType() == XmlResourceParser.START_TAG
                        && "base-config".equals(parser.getName())) {
                    assertTrue(parser.getAttributeBooleanValue(
                            null, "cleartextTrafficPermitted", false));
                    return;
                }
            }
        }

        throw new AssertionError("network security config has no base-config element");
    }
}
