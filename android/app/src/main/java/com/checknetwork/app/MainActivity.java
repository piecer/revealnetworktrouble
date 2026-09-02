package com.checknetwork.app;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.text.Editable;
import android.text.TextWatcher;
import android.view.LayoutInflater;
import android.view.View;
import android.widget.AdapterView;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.CheckBox;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.Spinner;
import android.widget.TextView;
import androidx.core.view.ViewCompat;
import com.checknetwork.app.core.ApiError;
import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.ContractLimits;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportMarkdownExporter;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.network.ApiConnectionConfig;
import com.checknetwork.app.network.TransportException;
import com.checknetwork.app.state.RequestState;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;

/** Native diagnostics UI backed only by the bounded transport/state contracts. */
public final class MainActivity extends Activity {
    private static final String KEY_API="form.api",KEY_TIMEOUT="form.timeout",KEY_KINDS="form.kinds",KEY_ADDRESSES="form.addresses",KEY_EXPECTED="form.expected",KEY_ATTEMPTS="form.attempts";
    private static final int MAX_TARGETS=ContractLimits.MAX_TARGETS;
    interface SessionFactory { DiagnosticsSession create(DiagnosticsSession.Dispatcher dispatcher); }
    interface RawShareFactory { RawReportShare create(Activity activity); }
    interface ConnectionConfigFactory { ApiConnectionConfig create(String base,boolean debug,String bearer); }
    private static final SessionFactory DEFAULT_SESSION_FACTORY=DiagnosticsSession::create;
    private static final RawShareFactory DEFAULT_RAW_SHARE_FACTORY=RawReportShare::new;
    private static final ConnectionConfigFactory DEFAULT_CONNECTION_CONFIG_FACTORY=ApiConnectionConfig::create;
    private static SessionFactory sessionFactory=DEFAULT_SESSION_FACTORY;
    private static RawShareFactory rawShareFactory=DEFAULT_RAW_SHARE_FACTORY;
    private static ConnectionConfigFactory connectionConfigFactory=DEFAULT_CONNECTION_CONFIG_FACTORY;
    static void setSessionFactoryForTests(SessionFactory factory){sessionFactory=Objects.requireNonNull(factory,"factory");}
    static void setRawShareFactoryForTests(RawShareFactory factory){rawShareFactory=Objects.requireNonNull(factory,"factory");}
    static void setConnectionConfigFactoryForTests(ConnectionConfigFactory factory){connectionConfigFactory=Objects.requireNonNull(factory,"factory");}
    static void resetSessionFactoryForTests(){sessionFactory=DEFAULT_SESSION_FACTORY;rawShareFactory=DEFAULT_RAW_SHARE_FACTORY;connectionConfigFactory=DEFAULT_CONNECTION_CONFIG_FACTORY;}

    private DiagnosticsSession session;
    private final Object owner=new Object();
    private Retained retained;
    private LinearLayout targets,report,results;
    private EditText apiUrl,timeout,bearer;
    private CheckBox authEnabled;
    private TextView error,status;
    private ProgressBar progress;
    private Button run,cancel,retry,share,shareRaw,addTarget;
    private Report readyReport;
    private ReadyRaw readyRaw;
    private RawReportShare rawReportShare;
    private AlertDialog rawWarningDialog;
    private boolean suppressInput;
    private boolean retainingSession;

    @Override protected void onCreate(Bundle savedInstanceState){
        super.onCreate(savedInstanceState);setContentView(R.layout.activity_main);bindViews();
        ViewCompat.setAccessibilityHeading(findViewById(R.id.hero_heading),true);
        ViewCompat.setAccessibilityHeading(findViewById(R.id.targets_heading),true);
        Object previous=getLastNonConfigurationInstance();
        retained=previous instanceof Retained?(Retained)previous:new Retained(sessionFactory.create(r->new Handler(Looper.getMainLooper()).post(r)),rawShareFactory.create(this));
        session=retained.session;rawReportShare=retained.rawReportShare;
        suppressInput=true;
        apiUrl.setSaveEnabled(false);timeout.setSaveEnabled(false);bearer.setSaveEnabled(false);
        bearer.setText(retained.bearer);authEnabled.setChecked(retained.authEnabled);
        findViewById(R.id.bearer_label).setVisibility(retained.authEnabled?View.VISIBLE:View.GONE);
        bearer.setVisibility(retained.authEnabled?View.VISIBLE:View.GONE);
        restoreForm(savedInstanceState);
        suppressInput=false;
        wireActions(); session.attach(owner,this::renderState);
    }

    private void bindViews(){
        targets=findViewById(R.id.targets);report=findViewById(R.id.report);results=findViewById(R.id.results);apiUrl=findViewById(R.id.api_url);timeout=findViewById(R.id.timeout);bearer=findViewById(R.id.bearer);authEnabled=findViewById(R.id.auth_enabled);
        error=findViewById(R.id.error);status=findViewById(R.id.request_status);progress=findViewById(R.id.progress);run=findViewById(R.id.run);cancel=findViewById(R.id.cancel);retry=findViewById(R.id.retry);share=findViewById(R.id.share);shareRaw=findViewById(R.id.share_raw);addTarget=findViewById(R.id.add_target);
    }

    private void restoreForm(Bundle state){
        String base=getString(R.string.default_api_base);int timeoutMs=ContractLimits.DEFAULT_TIMEOUT_MS;
        if(state!=null){base=bounded(state.getString(KEY_API,""));timeoutMs=state.getInt(KEY_TIMEOUT,ContractLimits.DEFAULT_TIMEOUT_MS);}
        apiUrl.setText(base);timeout.setText(String.valueOf(timeoutMs));
        ArrayList<String> kinds=state==null?null:state.getStringArrayList(KEY_KINDS),addresses=state==null?null:state.getStringArrayList(KEY_ADDRESSES),expected=state==null?null:state.getStringArrayList(KEY_EXPECTED),attempts=state==null?null:state.getStringArrayList(KEY_ATTEMPTS);
        int count=addresses==null?0:Math.min(MAX_TARGETS,addresses.size());
        for(int i=0;i<count;i++){
            try{addTargetRow(CheckKind.fromWire(kinds.get(i)),bounded(addresses.get(i)),bounded(expected.get(i)),bounded(attempts.get(i)));}catch(RuntimeException ignored){break;}
        }
        if(targets.getChildCount()==0)addTargetRow(CheckKind.DNS,"example.com","","");
    }
    private static String bounded(String value){if(value==null)return "";return value.length()>ContractLimits.MAX_STRING_CHARS?value.substring(0,ContractLimits.MAX_STRING_CHARS):value;}

    private void wireActions(){
        TextWatcher watcher=watcher();apiUrl.addTextChangedListener(watcher);timeout.addTextChangedListener(watcher);bearer.addTextChangedListener(watcher);
        authEnabled.setOnCheckedChangeListener((v,checked)->{findViewById(R.id.bearer_label).setVisibility(checked?View.VISIBLE:View.GONE);bearer.setVisibility(checked?View.VISIBLE:View.GONE);inputMutated();});
        addTarget.setOnClickListener(v->{if(targets.getChildCount()<MAX_TARGETS){addTargetRow(CheckKind.DNS,"","","");inputMutated();}});
        run.setOnClickListener(v->startDiagnostics());retry.setOnClickListener(v->startDiagnostics());cancel.setOnClickListener(v->{clearReady();session.cancel();});share.setOnClickListener(v->shareReadyReport());shareRaw.setOnClickListener(v->warnBeforeRawShare());
    }

    private TextWatcher watcher(){return new TextWatcher(){public void beforeTextChanged(CharSequence s,int a,int b,int c){}public void onTextChanged(CharSequence s,int a,int b,int c){inputMutated();}public void afterTextChanged(Editable e){}};}
    private void inputMutated(){if(suppressInput)return;clearReady();session.invalidateInput(safeSignature());}
    private String safeSignature(){try{return collectForm().signature();}catch(RuntimeException invalid){return "invalid-form";}}

    private void addTargetRow(CheckKind initial,String addressText,String expectedText,String attemptsText){
        View row=LayoutInflater.from(this).inflate(R.layout.target_row,targets,false);Spinner kind=row.findViewById(R.id.kind);EditText address=row.findViewById(R.id.address),expected=row.findViewById(R.id.expected_status),attempts=row.findViewById(R.id.attempts);
        ArrayAdapter<String> adapter=new ArrayAdapter<>(this,android.R.layout.simple_spinner_item,kindLabels());adapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item);kind.setAdapter(adapter);kind.setSaveEnabled(false);kind.setSelection(kindIndex(initial));
        address.setSaveEnabled(false);expected.setSaveEnabled(false);attempts.setSaveEnabled(false);address.setText(addressText);expected.setText(expectedText);attempts.setText(attemptsText);
        TextWatcher watcher=watcher();address.addTextChangedListener(watcher);expected.addTextChangedListener(watcher);attempts.addTextChangedListener(watcher);
        boolean[] initialSelection={true};
        kind.setOnItemSelectedListener(new AdapterView.OnItemSelectedListener(){public void onNothingSelected(AdapterView<?> p){}public void onItemSelected(AdapterView<?> p,View v,int position,long id){updateRow(row,CheckKind.values()[position]);if(initialSelection[0]){initialSelection[0]=false;return;}inputMutated();}});
        row.findViewById(R.id.remove).setOnClickListener(v->{if(targets.getChildCount()>1){targets.removeView(row);refreshRemoveDescriptions();inputMutated();}});
        targets.addView(row);updateRow(row,initial);refreshRemoveDescriptions();addTarget.setEnabled(targets.getChildCount()<MAX_TARGETS);
    }
    private void updateRow(View row,CheckKind kind){
        EditText address=row.findViewById(R.id.address),expected=row.findViewById(R.id.expected_status),attempts=row.findViewById(R.id.attempts);address.setHint(addressHint(kind));
        int http=kind.supportsExpectedStatus()?View.VISIBLE:View.GONE;expected.setVisibility(http);row.findViewById(R.id.expected_status_label).setVisibility(http);
        int trace=kind==CheckKind.TRACEROUTE?View.VISIBLE:View.GONE;attempts.setVisibility(trace);row.findViewById(R.id.attempts_label).setVisibility(trace);refreshRemoveDescriptions();
    }
    private void refreshRemoveDescriptions(){for(int i=0;i<targets.getChildCount();i++){View row=targets.getChildAt(i);Spinner spinner=row.findViewById(R.id.kind);EditText address=row.findViewById(R.id.address);String kind=spinner.getSelectedItem()==null?"":spinner.getSelectedItem().toString();row.findViewById(R.id.remove).setContentDescription(getString(R.string.remove_target,i+1,kind,address.getText().toString()));}}
    private List<String> kindLabels(){return List.of(getString(R.string.kind_dns),getString(R.string.kind_tcp),getString(R.string.kind_http),getString(R.string.kind_https),getString(R.string.kind_ssh),getString(R.string.kind_smtp),getString(R.string.kind_submission),getString(R.string.kind_smtps),getString(R.string.kind_imap),getString(R.string.kind_imaps),getString(R.string.kind_pop3),getString(R.string.kind_pop3s),getString(R.string.kind_traceroute));}
    private int addressHint(CheckKind kind){return switch(kind){case DNS->R.string.hint_dns;case TCP->R.string.hint_tcp;case HTTP->R.string.hint_http;case HTTPS->R.string.hint_https;case TRACEROUTE->R.string.hint_trace;default->R.string.hint_service;};}
    private static int kindIndex(CheckKind kind){return kind.ordinal();}

    private FormState collectForm(){
        final int timeoutMs;try{timeoutMs=Integer.parseInt(timeout.getText().toString().trim());}catch(NumberFormatException e){throw new IllegalArgumentException(getString(R.string.error_timeout));}
        List<FormState.Target> values=new ArrayList<>();
        for(int i=0;i<targets.getChildCount();i++){View row=targets.getChildAt(i);CheckKind kind=CheckKind.values()[((Spinner)row.findViewById(R.id.kind)).getSelectedItemPosition()];values.add(new FormState.Target(kind,((EditText)row.findViewById(R.id.address)).getText().toString(),((EditText)row.findViewById(R.id.expected_status)).getText().toString(),((EditText)row.findViewById(R.id.attempts)).getText().toString()));}
        return new FormState(apiUrl.getText().toString(),timeoutMs,values);
    }
    private void startDiagnostics(){
        hideError();clearReady();
        try{FormState form=collectForm();ReportRequest request=form.toRequest();String credential=authEnabled.isChecked()?bearer.getText().toString():null;ApiConnectionConfig config=connectionConfigFactory.create(form.apiBase(),(getApplicationInfo().flags&android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE)!=0,credential);session.start(config,request);}catch(IllegalArgumentException e){showError(humanInputError(e));}
    }
    private String humanInputError(IllegalArgumentException e){String message=e.getMessage();if(message!=null&&(message.contains("Timeout")||message.contains("timeout")))return getString(R.string.error_timeout);if(message!=null&&(message.contains("origin")||message.contains("URL")))return getString(R.string.error_api_base);return message==null?getString(R.string.error_target,1,"Invalid input"):message;}

    private void renderState(RequestState state){
        boolean loading=state.phase()==RequestState.Phase.LOADING;progress.setVisibility(loading?View.VISIBLE:View.GONE);cancel.setVisibility(loading?View.VISIBLE:View.GONE);run.setEnabled(!loading);retry.setVisibility(state.phase()==RequestState.Phase.ERROR||state.phase()==RequestState.Phase.CANCELLED?View.VISIBLE:View.GONE);
        switch(state.phase()){
            case IDLE->status.setText(R.string.state_idle);
            case LOADING->{status.setText(R.string.state_loading);clearReady();status.requestFocus();}
            case READY->{status.setText(R.string.state_ready);hideError();renderReady(state);report.requestFocus();}
            case ERROR->{status.setText(R.string.state_error);clearReady();showTransportError(state.error().orElseThrow());}
            case CANCELLED->{status.setText(R.string.state_cancelled);clearReady();status.requestFocus();}
        }
    }
    private void showTransportError(TransportException failure){
        if(failure.kind()==TransportException.Kind.API){ApiError api=failure.apiError().orElseThrow();int res=switch(api.status()){case 401->R.string.error_401;case 422->R.string.error_422;case 429->R.string.error_429;case 503->R.string.error_503;case 500->R.string.error_500;default->0;};String message=res==0?getString(R.string.error_http,api.status()):getString(res);if(api.retryAt().isPresent())message+="\n"+getString(R.string.retry_after,api.retryAt().orElseThrow().toString());showError(message);return;}
        int res=switch(failure.kind()){case NETWORK->R.string.error_network;case TIMEOUT->R.string.error_timeout_request;case RESPONSE_TOO_LARGE->R.string.error_response_large;default->R.string.error_invalid_response;};showError(getString(res));
    }
    private void showError(String message){error.setText(message);error.setVisibility(View.VISIBLE);error.requestFocus();}
    private void hideError(){error.setVisibility(View.GONE);}
    private void clearReady(){readyReport=null;readyRaw=null;if(rawWarningDialog!=null){rawWarningDialog.dismiss();rawWarningDialog=null;}rawReportShare.clear();results.removeAllViews();report.setVisibility(View.GONE);share.setEnabled(false);shareRaw.setEnabled(false);}
    private void renderReady(RequestState state){
        String raw=state.rawJson().orElse(null);
        readyRaw=state.shareEligible()&&raw!=null&&raw.getBytes(StandardCharsets.UTF_8).length<=ContractLimits.MAX_TRANSPORT_BYTES?new ReadyRaw(state.ownerId(),state.signature(),raw):null;
        renderReport(state.report().orElseThrow());
    }
    private void renderReport(Report value){readyReport=value;results.removeAllViews();for(AnalysisPresentation.Block block:AnalysisPresentation.from(value).blocks()){if(block.folded()){addFoldedBlock(block);continue;}TextView heading=new TextView(this);heading.setText(block.heading());heading.setTextColor(getColor(R.color.lime));heading.setTextSize(18);heading.setTypeface(null,android.graphics.Typeface.BOLD);ViewCompat.setAccessibilityHeading(heading,true);heading.setPadding(0,12,0,4);results.addView(heading);TextView body=new TextView(this);body.setText(block.body());body.setTextColor(getColor(R.color.ink));body.setTextSize(15);body.setPadding(0,0,0,12);results.addView(body);}report.setVisibility(View.VISIBLE);share.setEnabled(true);shareRaw.setEnabled(readyRaw!=null);}
    private void addFoldedBlock(AnalysisPresentation.Block block){
        Button toggle=new Button(this);toggle.setId(R.id.raw_results_toggle);toggle.setText(R.string.raw_results_show);toggle.setAllCaps(false);toggle.setMinHeight(dp(48));toggle.setPadding(dp(12),dp(12),dp(12),dp(12));
        TextView body=new TextView(this);body.setId(R.id.raw_results_body);body.setText(block.body());body.setTextColor(getColor(R.color.ink));body.setTextSize(15);body.setPadding(0,dp(8),0,dp(12));body.setVisibility(View.GONE);
        ViewCompat.setStateDescription(toggle,getString(R.string.raw_results_collapsed));
        toggle.setOnClickListener(v->{boolean expand=body.getVisibility()!=View.VISIBLE;body.setVisibility(expand?View.VISIBLE:View.GONE);toggle.setText(expand?R.string.raw_results_hide:R.string.raw_results_show);ViewCompat.setStateDescription(toggle,getString(expand?R.string.raw_results_expanded:R.string.raw_results_collapsed));});
        results.addView(toggle,new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT,LinearLayout.LayoutParams.WRAP_CONTENT));results.addView(body);
    }
    private int dp(int value){return Math.round(value*getResources().getDisplayMetrics().density);}
    void renderReadyForTest(String ignoredRaw,Report report){readyRaw=null;renderReport(report);}
    private void shareReadyReport(){if(readyReport==null)return;Intent send=new Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_SUBJECT,getString(R.string.share_subject)).putExtra(Intent.EXTRA_TEXT,ReportMarkdownExporter.export(readyReport));startActivity(Intent.createChooser(send,getString(R.string.share_chooser)));}
    private void warnBeforeRawShare(){
        ReadyRaw candidate=readyRaw;if(candidate==null||!shareRaw.isEnabled())return;
        long warnedOwner=candidate.ownerId();String warnedSignature=candidate.signature();
        AlertDialog warning=new AlertDialog.Builder(this).setTitle(R.string.raw_share_warning_title).setMessage(R.string.raw_share_warning_message)
                .setNegativeButton(R.string.raw_share_cancel,null).setPositiveButton(R.string.raw_share_confirm,(dialog,which)->shareConfirmedRaw(warnedOwner,warnedSignature)).show();
        rawWarningDialog=warning;warning.setOnDismissListener(dialog->{if(rawWarningDialog==warning)rawWarningDialog=null;});
    }
    private void shareConfirmedRaw(long warnedOwner,String warnedSignature){
        ReadyRaw candidate=readyRaw;if(candidate==null)return;
        RequestState current=session.state();
        if(candidate.ownerId()!=warnedOwner||!candidate.signature().equals(warnedSignature)||current.phase()!=RequestState.Phase.READY||current.ownerId()!=candidate.ownerId()||!current.signature().equals(candidate.signature())||!current.rawJson().filter(candidate.json()::equals).isPresent())return;
        try{
            Uri stream=rawReportShare.writeConfirmed(candidate.json());
            Intent send=new Intent(Intent.ACTION_SEND).setType("application/json").putExtra(Intent.EXTRA_STREAM,stream).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
            startActivity(Intent.createChooser(send,getString(R.string.raw_share_chooser)));
        }catch(IOException|IllegalArgumentException failure){showError(getString(R.string.raw_share_error));}
    }

    @Override protected void onSaveInstanceState(Bundle out){super.onSaveInstanceState(out);try{FormState form=collectForm();out.putString(KEY_API,bounded(form.apiBase()));out.putInt(KEY_TIMEOUT,form.timeoutMs());ArrayList<String> kinds=new ArrayList<>(),addresses=new ArrayList<>(),expected=new ArrayList<>(),attempts=new ArrayList<>();for(FormState.Target target:form.targets()){kinds.add(target.kind().wireValue());addresses.add(bounded(target.address()));expected.add(bounded(target.expectedStatus()));attempts.add(bounded(target.attempts()));}out.putStringArrayList(KEY_KINDS,kinds);out.putStringArrayList(KEY_ADDRESSES,addresses);out.putStringArrayList(KEY_EXPECTED,expected);out.putStringArrayList(KEY_ATTEMPTS,attempts);}catch(RuntimeException ignored){}}
    @Override public Object onRetainNonConfigurationInstance(){retainingSession=true;retained.bearer=bearer.getText().toString();retained.authEnabled=authEnabled.isChecked();return retained;}
    @Override protected void onDestroy(){session.detach(owner);if(!retainingSession&&!isChangingConfigurations()){rawReportShare.clear();session.destroy();}super.onDestroy();}
    private record ReadyRaw(long ownerId,String signature,String json){}
    private static final class Retained {final DiagnosticsSession session;final RawReportShare rawReportShare;String bearer="";boolean authEnabled;Retained(DiagnosticsSession session,RawReportShare rawReportShare){this.session=session;this.rawReportShare=rawReportShare;}}
}
