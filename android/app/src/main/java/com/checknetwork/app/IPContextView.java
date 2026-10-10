package com.checknetwork.app;

import android.content.Context;
import android.view.View;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;
import androidx.core.view.ViewCompat;
import com.checknetwork.app.core.IPContext;
import com.checknetwork.app.core.IPContextAddresses;
import com.checknetwork.app.core.Report;
import java.util.List;
import java.util.Map;
import java.util.function.BooleanSupplier;

/** One lazy panel and reusable previous/next selector, never one view per observed IP. */
final class IPContextView extends LinearLayout {
    private final IPContextAddresses addresses;
    private final IPContextSession session;
    private final BooleanSupplier current;
    private final Button toggle;
    private LinearLayout panel;
    private TextView identity, body, status;
    private Button previous, next, query;
    private int selected;
    IPContextView(Context context, Report report, IPContextSession session, BooleanSupplier current) {
        super(context); this.addresses = IPContextAddresses.from(report); this.session = session; this.current = current;
        setTag("ip_context_view"); setOrientation(VERTICAL); setSaveEnabled(false);
        toggle = button("ip_context_toggle", R.string.ip_context_show);
        toggle.setOnClickListener(ignored -> {
            if (!current.getAsBoolean()) return;
            if (panel == null) expand();
            else {
                session.cancel(); panel.removeAllViews(); removeView(panel); panel = null;
                identity = null; body = null; status = null; previous = null; next = null; query = null;
                toggle.setText(R.string.ip_context_show);
                ViewCompat.setStateDescription(toggle, getContext().getString(R.string.raw_results_collapsed));
            }
        });
        ViewCompat.setStateDescription(toggle, context.getString(R.string.raw_results_collapsed)); addView(toggle);
    }
    private void expand() {
        panel = new LinearLayout(getContext()); panel.setOrientation(VERTICAL); panel.setSaveEnabled(false);
        TextView warning = text(); warning.setText(R.string.ip_context_warning); panel.addView(warning);
        TextView summary = text(); summary.setText(getContext().getString(R.string.ip_context_count, addresses.addresses().size(), addresses.omitted())); panel.addView(summary);
        if (!addresses.addresses().isEmpty()) {
            selected = Math.max(0, addresses.addresses().indexOf(session.state().address()));
            identity = text(); identity.setTag("ip_context_address"); identity.setTextIsSelectable(true); panel.addView(identity);
            previous = button("ip_context_previous", R.string.ip_context_previous);
            next = button("ip_context_next", R.string.ip_context_next);
            previous.setOnClickListener(v -> move(-1)); next.setOnClickListener(v -> move(1));
            panel.addView(previous); panel.addView(next);
            query = button("ip_context_query", R.string.ip_context_query);
            query.setOnClickListener(v -> { if (current.getAsBoolean()) { session.lookup(); render(session.state()); } }); panel.addView(query);
            status = text(); status.setTag("ip_context_status"); status.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE); panel.addView(status);
            body = text(); body.setTag("ip_context_body"); body.setTextIsSelectable(true); body.setAutoLinkMask(0); panel.addView(body);
            select();
        } else { TextView empty = text(); empty.setText(R.string.ip_context_empty); panel.addView(empty); }
        addView(panel); toggle.setText(R.string.ip_context_hide);
        ViewCompat.setStateDescription(toggle, getContext().getString(R.string.raw_results_expanded));
    }
    private void move(int delta) {
        if (!current.getAsBoolean() || panel == null) return;
        int nextIndex = selected + delta;
        if (nextIndex < 0 || nextIndex >= addresses.addresses().size()) return;
        selected = nextIndex; select();
    }
    private void select() {
        String address = addresses.addresses().get(selected);
        identity.setText(getContext().getString(R.string.ip_context_selected, selected + 1, addresses.addresses().size(), address));
        previous.setEnabled(selected > 0); next.setEnabled(selected + 1 < addresses.addresses().size());
        session.select(address); render(session.state());
    }
    void render(IPContextSession.Snapshot snapshot) {
        if (!current.getAsBoolean() || panel == null || body == null || !addresses.addresses().get(selected).equals(snapshot.address())) return;
        query.setEnabled(!snapshot.loading());
        int label = snapshot.loading() ? R.string.ip_context_loading : snapshot.context() != null ? R.string.ip_context_ready : R.string.ip_context_idle;
        if (snapshot.failure() != null) label = switch (snapshot.failure()) {
            case UNSUPPORTED -> R.string.ip_context_unsupported;
            case EXPIRED -> R.string.ip_context_expired;
            case TIMEOUT -> R.string.ip_context_timeout;
            case INVALID, TOO_LARGE -> R.string.ip_context_invalid;
            case CANCELLED -> R.string.ip_context_idle;
            case NETWORK, UNAVAILABLE -> R.string.ip_context_unavailable;
        };
        status.setText(label);
        body.setText(snapshot.context() == null ? "" : describe(snapshot.context(), snapshot.appCache()));
    }
    private String describe(IPContext value, boolean appCache) {
        Map<String,Object> root = value.projection(); StringBuilder out = new StringBuilder();
        line(out, R.string.ip_context_source, root.get("source"));
        line(out, R.string.ip_context_fetched, root.get("fetched_at"));
        line(out, R.string.ip_context_expires, root.get("expires_at"));
        out.append(getContext().getString(appCache ? R.string.ip_context_cache : R.string.ip_context_network)).append('\n');
        section(out, R.string.ip_context_ptr, R.string.ip_context_ptr_caveat);
        Map<?,?> reverse = map(root.get("reverse_dns")); provenance(out, reverse);
        for (Object item : list(reverse.get("names"))) {
            Map<?,?> name = map(item); line(out, R.string.ip_context_name, name.get("name"));
            line(out, R.string.ip_context_forward, statusLabel(name.get("forward_status")));
        }
        line(out, R.string.ip_context_omitted, reverse.get("omitted"));
        section(out, R.string.ip_context_rdap, R.string.ip_context_rdap_caveat);
        Map<?,?> registration = map(root.get("registration")); provenance(out, registration);
        String[] fields = {"registry", "start_address", "end_address", "handle", "name", "type", "country", "organization", "registered_at", "updated_at"};
        int[] labels = {R.string.ip_context_registry, R.string.ip_context_start, R.string.ip_context_end, R.string.ip_context_handle, R.string.ip_context_name, R.string.ip_context_type, R.string.ip_context_country, R.string.ip_context_org, R.string.ip_context_registered, R.string.ip_context_updated};
        for (int i = 0; i < fields.length; i++) if (registration.containsKey(fields[i])) line(out, labels[i], registration.get(fields[i]));
        section(out, R.string.ip_context_bgp, R.string.ip_context_bgp_caveat);
        Map<?,?> routing = map(root.get("routing")); provenance(out, routing);
        if (routing.containsKey("prefix")) line(out, R.string.ip_context_prefix, routing.get("prefix"));
        line(out, R.string.ip_context_omitted, routing.get("omitted"));
        section(out, R.string.ip_context_rpki, R.string.ip_context_rpki_caveat);
        for (Object item : list(routing.get("origins"))) {
            Map<?,?> origin = map(item); line(out, R.string.ip_context_asn, origin.get("asn"));
            Map<?,?> rpki = map(origin.get("rpki")); provenance(out, rpki);
            line(out, R.string.ip_context_checked, rpki.get("checked_at"));
            if (rpki.containsKey("validity")) line(out, R.string.ip_context_validity, statusLabel(rpki.get("validity")));
        }
        return out.toString();
    }
    private void section(StringBuilder out, int heading, int caveat) { out.append('\n').append(getContext().getString(heading)).append('\n').append(getContext().getString(caveat)).append('\n'); }
    private void provenance(StringBuilder out, Map<?,?> bundle) {
        line(out, R.string.ip_context_status_label, statusLabel(bundle.get("status")));
        line(out, R.string.ip_context_source, bundle.get("source"));
        if (bundle.containsKey("fetched_at")) line(out, R.string.ip_context_fetched, bundle.get("fetched_at"));
    }
    private String statusLabel(Object code) {
        int label = switch (String.valueOf(code)) {
            case "ok" -> R.string.ip_context_ok; case "limited" -> R.string.ip_context_limited;
            case "not_found" -> R.string.ip_context_not_found; case "timeout" -> R.string.ip_context_timed_out;
            case "unavailable" -> R.string.ip_context_failed; case "invalid_response" -> R.string.ip_context_bad_component;
            case "rate_limited" -> R.string.ip_context_rate_limited; case "confirmed" -> R.string.ip_context_confirmed;
            case "mismatch" -> R.string.ip_context_mismatch; case "valid" -> R.string.ip_context_valid;
            case "invalid_asn" -> R.string.ip_context_invalid_asn; case "invalid_length" -> R.string.ip_context_invalid_length;
            case "unknown" -> R.string.ip_context_unknown; default -> throw new IllegalArgumentException("Unvalidated IP context status");
        };
        return getContext().getString(label);
    }
    private void line(StringBuilder out, int label, Object value) { out.append(getContext().getString(label)).append(": ").append(value).append('\n'); }
    private static Map<?,?> map(Object value) { return (Map<?,?>) value; }
    private static List<?> list(Object value) { return (List<?>) value; }
    private Button button(String tag, int label) {
        Button button = new Button(getContext()); button.setTag(tag); button.setText(label); button.setAllCaps(false);
        button.setMinHeight(Math.round(48 * getResources().getDisplayMetrics().density)); button.setSaveEnabled(false);
        button.setLayoutParams(new LayoutParams(LayoutParams.MATCH_PARENT, LayoutParams.WRAP_CONTENT)); return button;
    }
    private TextView text() {
        TextView text = new TextView(getContext()); text.setTextColor(getContext().getColor(R.color.ink)); text.setTextSize(15);
        text.setSaveEnabled(false); text.setPadding(0, 8, 0, 12);
        text.setLayoutParams(new LayoutParams(LayoutParams.MATCH_PARENT, LayoutParams.WRAP_CONTENT)); return text;
    }
}
