package com.checknetwork.app;

import android.content.Context;
import android.view.View;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;
import androidx.core.view.ViewCompat;
import com.checknetwork.app.core.GeoDetails;
import java.util.function.BooleanSupplier;

/** One lazy, owner-fenced raw metadata disclosure; never mounts a row per address. */
final class GeoDetailsView extends LinearLayout {
    private final GeoDetails details;
    private final BooleanSupplier current;
    private final Button toggle;
    private LinearLayout panel;
    private int selected;

    GeoDetailsView(Context context, GeoDetails details, BooleanSupplier current) {
        super(context);
        this.details = details;
        this.current = current;
        setOrientation(VERTICAL);
        setSaveEnabled(false);
        toggle = button("geo_details_toggle", "Show supplemental location details (sensitive identifiers)");
        ViewCompat.setStateDescription(toggle, "Collapsed");
        toggle.setOnClickListener(ignored -> {
            if (!current.getAsBoolean()) return;
            if (panel == null) expand();
            else {
                panel.removeAllViews();
                removeView(panel);
                panel = null;
                toggle.setText("Show supplemental location details (sensitive identifiers)");
                ViewCompat.setStateDescription(toggle, "Collapsed");
            }
        });
        addView(toggle);
    }

    private void expand() {
        selected = 0;
        panel = new LinearLayout(getContext());
        panel.setOrientation(VERTICAL);
        panel.setSaveEnabled(false);
        TextView summary = text("Supplemental metadata from raw observed addresses; entries may be absent from the compact graph. "
                + "Approximate GeoIP, not a measured position. This snapshot does not certify legacy coordinates or ASN."
                + "\nAvailable: " + details.entries().size() + " · Total: " + details.total() + " · Omitted: " + details.omitted());
        panel.addView(summary);
        if (details.entries().isEmpty()) panel.addView(text("No supplemental metadata entries were returned."));
        else {
            TextView body = text("");
            body.setTag("geo_details_body");
            body.setTextIsSelectable(true);
            Button previous = button("geo_details_previous", "Previous address");
            Button next = button("geo_details_next", "Next address");
            Runnable render = () -> {
                if (!current.getAsBoolean()) return;
                body.setText(entryText(details.entries().get(selected), selected + 1, details.entries().size()));
                previous.setEnabled(selected > 0);
                next.setEnabled(selected + 1 < details.entries().size());
            };
            previous.setOnClickListener(ignored -> {
                if (current.getAsBoolean() && selected > 0) { selected--; render.run(); }
            });
            next.setOnClickListener(ignored -> {
                if (current.getAsBoolean() && selected + 1 < details.entries().size()) { selected++; render.run(); }
            });
            panel.addView(previous);
            panel.addView(next);
            panel.addView(body);
            render.run();
        }
        addView(panel);
        toggle.setText("Hide supplemental location details");
        ViewCompat.setStateDescription(toggle, "Expanded");
    }

    private static String entryText(GeoDetails.Entry entry, int index, int count) {
        StringBuilder text = new StringBuilder("Address ").append(index).append(" of ").append(count);
        String[][] fields = {{"address", "Address"}, {"city", "City"}, {"region", "Region"},
                {"country", "Country"}, {"country_code", "Country code"}, {"continent", "Continent"},
                {"continent_code", "Continent code"}, {"region_code", "Region code"}, {"postal", "Postal"},
                {"timezone", "Timezone"}, {"isp", "ISP (not AS organization)"}, {"network_domain", "Network domain"},
                {"provider", "Provider"}, {"source", "Source"}, {"fetched_at", "Fetched at (local lookup)"},
                {"expires_at", "Cache expires at"}};
        for (String[] field : fields) text.append('\n').append(field[1]).append(": ")
                .append(entry.fields().getOrDefault(field[0], "not supplied"));
        text.append("\nProvider database update: not supplied");
        // Validated entry text is <=1536 UTF-8 bytes; all fixed labels fit in the remaining reserve.
        if (text.length() > 4096) throw new IllegalStateException("Supplemental detail presentation limit");
        return text.toString();
    }

    private Button button(String tag, String label) {
        Button result = new Button(getContext());
        result.setTag(tag);
        result.setText(label);
        result.setAllCaps(false);
        result.setMinHeight(Math.round(48 * getResources().getDisplayMetrics().density));
        result.setSaveEnabled(false);
        result.setLayoutParams(new LayoutParams(LayoutParams.MATCH_PARENT, LayoutParams.WRAP_CONTENT));
        return result;
    }

    private TextView text(String value) {
        TextView result = new TextView(getContext());
        result.setText(value);
        result.setTextColor(getContext().getColor(R.color.ink));
        result.setTextSize(15);
        result.setSaveEnabled(false);
        result.setPadding(0, 8, 0, 12);
        return result;
    }
}
