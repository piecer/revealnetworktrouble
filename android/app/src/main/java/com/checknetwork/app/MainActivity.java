package com.checknetwork.app;

import android.content.Intent;
import android.content.SharedPreferences;
import android.graphics.Color;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.LayoutInflater;
import android.view.View;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.Spinner;
import android.widget.TextView;

import android.app.Activity;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.MalformedURLException;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.Locale;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public final class MainActivity extends Activity {
    private static final String PREFS = "checknetwork";
    private static final String API_URL_KEY = "api_url";
    private static final int MIN_TIMEOUT_MS = 100;
    private static final int MAX_TIMEOUT_MS = 30_000;
    private static final int MAX_TARGETS = 20;

    private final ExecutorService executor = Executors.newSingleThreadExecutor();
    private final Handler main = new Handler(Looper.getMainLooper());
    private LinearLayout targets;
    private LinearLayout report;
    private LinearLayout results;
    private EditText apiUrl;
    private EditText timeout;
    private TextView error;
    private TextView reportStatus;
    private TextView reportSummary;
    private ProgressBar progress;
    private Button run;
    private Button addTarget;
    private String currentReport;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);

        targets = findViewById(R.id.targets);
        report = findViewById(R.id.report);
        results = findViewById(R.id.results);
        apiUrl = findViewById(R.id.api_url);
        timeout = findViewById(R.id.timeout);
        error = findViewById(R.id.error);
        reportStatus = findViewById(R.id.report_status);
        reportSummary = findViewById(R.id.report_summary);
        progress = findViewById(R.id.progress);
        run = findViewById(R.id.run);
        addTarget = findViewById(R.id.add_target);

        SharedPreferences prefs = getSharedPreferences(PREFS, MODE_PRIVATE);
        apiUrl.setText(prefs.getString(API_URL_KEY, "http://10.0.2.2:8080"));

        addTarget.setOnClickListener(v -> {
            if (targets.getChildCount() >= MAX_TARGETS) {
                showError("검사 대상은 최대 20개까지 추가할 수 있습니다.");
                return;
            }
            hideError();
            addTarget("dns", "");
        });
        run.setOnClickListener(v -> runDiagnostics());
        findViewById(R.id.share).setOnClickListener(v -> shareReport());

        addTarget("dns", "example.com");
        addTarget("tcp", "1.1.1.1:443");
        addTarget("http", "https://example.com");
    }

    private void addTarget(String selectedKind, String initialAddress) {
        View row = LayoutInflater.from(this).inflate(R.layout.target_row, targets, false);
        Spinner kind = row.findViewById(R.id.kind);
        EditText address = row.findViewById(R.id.address);
        String[] kinds = {"DNS", "TCP", "HTTP"};
        ArrayAdapter<String> adapter = new ArrayAdapter<>(this, android.R.layout.simple_spinner_item, kinds);
        adapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item);
        kind.setAdapter(adapter);
        kind.setSelection(Math.max(0, adapter.getPosition(selectedKind.toUpperCase(Locale.ROOT))));
        address.setText(initialAddress);
        row.findViewById(R.id.remove).setOnClickListener(v -> {
            if (targets.getChildCount() > 1) {
                targets.removeView(row);
                updateAddTargetState();
            }
        });
        targets.addView(row);
        updateAddTargetState();
    }

    private void runDiagnostics() {
        hideError();
        final String baseUrl;
        final int timeoutMs;
        final JSONObject request;
        try {
            baseUrl = normalizeApiUrl(apiUrl.getText().toString());
            timeoutMs = parseTimeout(timeout.getText().toString());
            request = buildRequest(timeoutMs);
        } catch (IllegalArgumentException | JSONException exception) {
            showError(exception.getMessage());
            return;
        }

        getSharedPreferences(PREFS, MODE_PRIVATE).edit().putString(API_URL_KEY, baseUrl).apply();
        setLoading(true);
        final int targetCount = request.optJSONArray("targets").length();
        executor.execute(() -> {
            try {
                String response = postReport(baseUrl, request.toString(), timeoutMs, targetCount);
                JSONObject json = new JSONObject(response);
                main.post(() -> {
                    setLoading(false);
                    renderReport(json, response);
                });
            } catch (Exception exception) {
                main.post(() -> {
                    setLoading(false);
                    showError(humanizeError(exception));
                });
            }
        });
    }

    private JSONObject buildRequest(int timeoutMs) throws JSONException {
        JSONArray requestTargets = new JSONArray();
        for (int index = 0; index < targets.getChildCount(); index++) {
            View row = targets.getChildAt(index);
            Spinner kindView = row.findViewById(R.id.kind);
            EditText addressView = row.findViewById(R.id.address);
            String kind = kindView.getSelectedItem().toString().toLowerCase(Locale.ROOT);
            String address = addressView.getText().toString().trim();
            if (address.isEmpty()) throw new IllegalArgumentException((index + 1) + "번째 대상 주소를 입력해 주세요.");
            JSONObject target = new JSONObject().put("kind", kind).put("address", address);
            if ("http".equals(kind)) target.put("expected_status", 200);
            requestTargets.put(target);
        }
        return new JSONObject().put("timeout_ms", timeoutMs).put("targets", requestTargets);
    }

    private String postReport(String baseUrl, String body, int timeoutMs, int targetCount) throws IOException, JSONException {
        HttpURLConnection connection = (HttpURLConnection) new URL(baseUrl + "/api/v1/reports").openConnection();
        connection.setRequestMethod("POST");
        connection.setRequestProperty("Content-Type", "application/json; charset=utf-8");
        connection.setRequestProperty("Accept", "application/json");
        connection.setConnectTimeout(timeoutMs + 2_000);
        connection.setReadTimeout((timeoutMs * Math.max(1, targetCount)) + 5_000);
        connection.setDoOutput(true);
        try {
            try (OutputStream stream = connection.getOutputStream()) {
                stream.write(body.getBytes(StandardCharsets.UTF_8));
            }
            int status = connection.getResponseCode();
            String response = readAll(status >= 200 && status < 300 ? connection.getInputStream() : connection.getErrorStream());
            if (status < 200 || status >= 300) {
                String message = "API 오류 (" + status + ")";
                try {
                    message = new JSONObject(response).getJSONObject("error").optString("message", message);
                } catch (JSONException ignored) { }
                throw new IOException(message);
            }
            return response;
        } finally {
            connection.disconnect();
        }
    }

    private void renderReport(JSONObject json, String raw) {
        try {
            currentReport = new JSONObject(raw).toString(2);
            String status = json.getString("status");
            JSONObject summary = json.getJSONObject("summary");
            boolean healthy = "healthy".equals(status);
            reportStatus.setText(healthy ? "● 정상" : "● " + ("degraded".equals(status) ? "일부 장애" : "연결 불가"));
            reportStatus.setTextColor(Color.parseColor(healthy ? "#C9FF46" : "#FF725E"));
            reportSummary.setText(String.format(Locale.KOREA, "전체 %d  ·  성공 %d  ·  실패 %d  ·  %d ms",
                    summary.getInt("total"), summary.getInt("passed"), summary.getInt("failed"), json.getLong("duration_ms")));

            results.removeAllViews();
            JSONArray items = json.getJSONArray("results");
            for (int index = 0; index < items.length(); index++) addResult(items.getJSONObject(index));
            report.setVisibility(View.VISIBLE);
        } catch (JSONException exception) {
            showError("서버 응답 형식을 해석할 수 없습니다.");
        }
    }

    private void addResult(JSONObject item) throws JSONException {
        boolean passed = "healthy".equals(item.getString("status"));
        LinearLayout card = new LinearLayout(this);
        card.setOrientation(LinearLayout.VERTICAL);
        card.setPadding(0, dp(14), 0, dp(14));

        TextView heading = new TextView(this);
        heading.setText(item.getString("kind").toUpperCase(Locale.ROOT) + "   " + (passed ? "PASS" : "FAIL"));
        heading.setTextColor(Color.parseColor(passed ? "#C9FF46" : "#FF725E"));
        heading.setTextSize(12);
        heading.setTypeface(null, android.graphics.Typeface.BOLD);
        card.addView(heading);

        TextView address = new TextView(this);
        address.setText(item.getString("address"));
        address.setTextColor(Color.parseColor("#F4F1E8"));
        address.setTextSize(15);
        address.setPadding(0, dp(5), 0, 0);
        card.addView(address);

        TextView detail = new TextView(this);
        String message = item.optString("message", passed ? "연결과 응답이 정상입니다." : "검사에 실패했습니다.");
        detail.setText(message + "  ·  " + item.optLong("latency_ms") + " ms");
        detail.setTextColor(Color.parseColor("#A9ABA2"));
        detail.setTextSize(12);
        detail.setPadding(0, dp(3), 0, 0);
        card.addView(detail);
        results.addView(card);
    }

    private String normalizeApiUrl(String value) {
        String normalized = value.trim().replaceAll("/+$", "");
        final URL parsed;
        try {
            parsed = new URL(normalized);
        } catch (MalformedURLException exception) {
            throw new IllegalArgumentException("API 서버 주소는 http:// 또는 https://로 시작해야 합니다.");
        }
        if (!("https".equals(parsed.getProtocol()) || "http".equals(parsed.getProtocol()))
                || parsed.getHost().isEmpty()
                || parsed.getUserInfo() != null
                || parsed.getQuery() != null
                || parsed.getRef() != null
                || !(parsed.getPath().isEmpty() || "/".equals(parsed.getPath()))) {
            throw new IllegalArgumentException("API 서버의 기본 주소만 입력해 주세요. 예: https://api.example.com");
        }
        return normalized;
    }

    private void updateAddTargetState() {
        addTarget.setEnabled(targets.getChildCount() < MAX_TARGETS);
    }

    private int parseTimeout(String value) {
        try {
            int parsed = Integer.parseInt(value.trim());
            if (parsed < MIN_TIMEOUT_MS || parsed > MAX_TIMEOUT_MS) throw new NumberFormatException();
            return parsed;
        } catch (NumberFormatException exception) {
            throw new IllegalArgumentException("제한 시간은 100~30000ms 사이여야 합니다.");
        }
    }

    private static String readAll(InputStream input) throws IOException {
        if (input == null) return "";
        StringBuilder result = new StringBuilder();
        try (BufferedReader reader = new BufferedReader(new InputStreamReader(input, StandardCharsets.UTF_8))) {
            String line;
            while ((line = reader.readLine()) != null) result.append(line);
        }
        return result.toString();
    }

    private String humanizeError(Exception exception) {
        String message = exception.getMessage();
        if (message == null || message.trim().isEmpty()) return "서버에 연결할 수 없습니다. 주소와 네트워크를 확인해 주세요.";
        return "진단을 완료하지 못했습니다: " + message;
    }

    private void shareReport() {
        if (currentReport == null) return;
        Intent intent = new Intent(Intent.ACTION_SEND);
        intent.setType("application/json");
        intent.putExtra(Intent.EXTRA_SUBJECT, "CheckNetwork 진단 리포트");
        intent.putExtra(Intent.EXTRA_TEXT, currentReport);
        startActivity(Intent.createChooser(intent, "리포트 공유"));
    }

    private void setLoading(boolean loading) {
        run.setEnabled(!loading);
        run.setText(loading ? "진단 중…" : "진단 시작  →");
        progress.setVisibility(loading ? View.VISIBLE : View.GONE);
    }

    private void showError(String message) {
        error.setText(message);
        error.setVisibility(View.VISIBLE);
    }

    private void hideError() {
        error.setVisibility(View.GONE);
    }

    private int dp(int value) {
        return Math.round(value * getResources().getDisplayMetrics().density);
    }

    @Override
    protected void onDestroy() {
        executor.shutdownNow();
        super.onDestroy();
    }
}
