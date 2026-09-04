package com.checknetwork.app;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.ActivityNotFoundException;
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
import com.checknetwork.app.core.CheckCapabilities;
import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.ContractLimits;
import com.checknetwork.app.core.Report;
import com.checknetwork.app.core.ReportMarkdownExporter;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.network.ApiConnectionConfig;
import com.checknetwork.app.network.TransportException;
import com.checknetwork.app.state.RequestState;
import java.time.Instant;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;

/** Native diagnostics UI backed only by the bounded transport/state contracts. */
public final class MainActivity extends Activity {
    private static final String KEY_API="form.api",KEY_TIMEOUT="form.timeout",KEY_KINDS="form.kinds",KEY_ADDRESSES="form.addresses",KEY_EXPECTED="form.expected",KEY_ATTEMPTS="form.attempts";
    private static final int MAX_TARGETS=ContractLimits.MAX_TARGETS;
    private static final int MAX_REMOVE_ADDRESS_CODE_POINTS=48;
    interface SessionFactory { DiagnosticsSession create(DiagnosticsSession.Dispatcher dispatcher); }
    interface RawRuntimeFactory { RawShareRuntime create(Activity activity); }
    interface ChooserLauncher { void launch(Activity activity,Intent chooser); }
    interface ConnectionConfigFactory { ApiConnectionConfig create(String base,boolean debug,String bearer); }
    enum PresentationPhase { EMPTY, RENDERING, RENDERED, RENDER_ERROR }
    enum RenderPoint { BEFORE_FIRST_VIEW, MID_BUILD }
    enum CommitPoint { PRE_SWAP, REPORT_VISIBILITY, READY_STATUS, FOCUS, LIVE_ANNOUNCEMENT, SHARE_ELIGIBILITY }
    interface RenderCheckpoint { void at(RenderPoint point); }
    interface PresentationRenderer { View render(MainActivity activity,AnalysisPresentation presentation,RenderCheckpoint checkpoint); }
    interface PresentationCommitter {
        void checkpoint(CommitPoint point);
        void swap(LinearLayout target,View candidate);
        void rollback(LinearLayout target);
    }
    private static final SessionFactory DEFAULT_SESSION_FACTORY=DiagnosticsSession::create;
    private static final RawRuntimeFactory DEFAULT_RAW_RUNTIME_FACTORY=RawReportShare::processRuntime;
    private static final ChooserLauncher DEFAULT_CHOOSER_LAUNCHER=Activity::startActivity;
    private static final ConnectionConfigFactory DEFAULT_CONNECTION_CONFIG_FACTORY=ApiConnectionConfig::create;
    private static final PresentationRenderer DEFAULT_PRESENTATION_RENDERER=MainActivity::buildDetachedPresentation;
    private static final PresentationCommitter DEFAULT_PRESENTATION_COMMITTER=new PresentationCommitter(){
        public void checkpoint(CommitPoint ignored){}
        public void swap(LinearLayout target,View candidate){target.removeAllViews();target.addView(candidate);}
        public void rollback(LinearLayout target){target.removeAllViews();}
    };
    private static SessionFactory sessionFactory=DEFAULT_SESSION_FACTORY;
    private static RawRuntimeFactory rawRuntimeFactory=DEFAULT_RAW_RUNTIME_FACTORY;
    private static ChooserLauncher chooserLauncher=DEFAULT_CHOOSER_LAUNCHER;
    private static ConnectionConfigFactory connectionConfigFactory=DEFAULT_CONNECTION_CONFIG_FACTORY;
    private static PresentationRenderer presentationRenderer=DEFAULT_PRESENTATION_RENDERER;
    private static PresentationCommitter presentationCommitter=DEFAULT_PRESENTATION_COMMITTER;
    static void setSessionFactoryForTests(SessionFactory factory){sessionFactory=Objects.requireNonNull(factory,"factory");}
    static void setRawRuntimeFactoryForTests(RawRuntimeFactory factory){rawRuntimeFactory=Objects.requireNonNull(factory,"factory");}
    static void setChooserLauncherForTests(ChooserLauncher launcher){chooserLauncher=Objects.requireNonNull(launcher,"launcher");}
    static void setConnectionConfigFactoryForTests(ConnectionConfigFactory factory){connectionConfigFactory=Objects.requireNonNull(factory,"factory");}
    static void setPresentationRendererForTests(PresentationRenderer renderer){presentationRenderer=Objects.requireNonNull(renderer,"renderer");}
    static PresentationRenderer defaultPresentationRendererForTests(){return DEFAULT_PRESENTATION_RENDERER;}
    static void setPresentationCommitterForTests(PresentationCommitter committer){presentationCommitter=Objects.requireNonNull(committer,"committer");}
    static PresentationCommitter defaultPresentationCommitterForTests(){return DEFAULT_PRESENTATION_COMMITTER;}
    static void resetSessionFactoryForTests(){sessionFactory=DEFAULT_SESSION_FACTORY;rawRuntimeFactory=DEFAULT_RAW_RUNTIME_FACTORY;chooserLauncher=DEFAULT_CHOOSER_LAUNCHER;connectionConfigFactory=DEFAULT_CONNECTION_CONFIG_FACTORY;presentationRenderer=DEFAULT_PRESENTATION_RENDERER;presentationCommitter=DEFAULT_PRESENTATION_COMMITTER;}

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
    private RawShareRuntime rawRuntime;
    private PendingRawShare pendingRawShare;
    private AlertDialog rawWarningDialog;
    private boolean rawAttached;
    private long rawAttachmentGeneration;
    private boolean suppressInput;
    private boolean retainingSession;
    private PresentationPhase presentationPhase=PresentationPhase.EMPTY;
    private long presentationGeneration;
    private View installedCandidate;
    private long installedCandidateGeneration=-1;
    private PresentationOwner installedCandidateOwner;

    @Override protected void onCreate(Bundle savedInstanceState){
        super.onCreate(savedInstanceState);setContentView(R.layout.activity_main);bindViews();
        ViewCompat.setAccessibilityHeading(findViewById(R.id.hero_heading),true);
        ViewCompat.setAccessibilityHeading(findViewById(R.id.targets_heading),true);
        Object previous=getLastNonConfigurationInstance();
        retained=previous instanceof Retained?(Retained)previous:new Retained(sessionFactory.create(r->new Handler(Looper.getMainLooper()).post(r)));
        session=retained.session;rawRuntime=rawRuntimeFactory.create(this);
        suppressInput=true;
        apiUrl.setSaveEnabled(false);timeout.setSaveEnabled(false);bearer.setSaveEnabled(false);
        bearer.setText(retained.bearer);authEnabled.setChecked(retained.authEnabled);
        findViewById(R.id.bearer_label).setVisibility(retained.authEnabled?View.VISIBLE:View.GONE);
        bearer.setVisibility(retained.authEnabled?View.VISIBLE:View.GONE);
        restoreForm(savedInstanceState);
        suppressInput=false;
        wireActions();session.attach(owner,this::renderState);attachRawRuntime();
    }

    @Override protected void onResume(){
        super.onResume();
        RawShareRuntime observedRuntime=rawRuntime;Object observedOwner=retained==null?null:retained.rawOwner;long observedGeneration=rawAttachmentGeneration;
        if(!isCurrentRawAttachment(observedRuntime,observedOwner,observedGeneration))return;
        try{observedRuntime.observe();}
        catch(RuntimeException failure){if(isCurrentRawAttachment(observedRuntime,observedOwner,observedGeneration))showRawShareFailure();}
    }

    private void attachRawRuntime(){rawRuntime.attach(retained.rawOwner,this::renderRawShareState);rawAttached=true;rawAttachmentGeneration++;}
    private void detachRawRuntime(){if(!rawAttached)return;rawAttached=false;rawAttachmentGeneration++;rawRuntime.detach(retained.rawOwner);}
    private boolean isCurrentRawAttachment(RawShareRuntime expectedRuntime,Object expectedOwner,long expectedGeneration){return rawAttached&&rawRuntime==expectedRuntime&&retained!=null&&retained.rawOwner==expectedOwner&&rawAttachmentGeneration==expectedGeneration&&!isFinishing()&&!isDestroyed();}

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
    private TextWatcher addressWatcher(){return new TextWatcher(){public void beforeTextChanged(CharSequence s,int a,int b,int c){}public void onTextChanged(CharSequence s,int a,int b,int c){refreshRemoveDescriptions();inputMutated();}public void afterTextChanged(Editable e){}};}
    private void inputMutated(){if(suppressInput)return;clearReady();session.invalidateInput(safeSignature());}
    private String safeSignature(){try{return collectForm().signature();}catch(RuntimeException invalid){return "invalid-form";}}

    private void addTargetRow(CheckKind initial,String addressText,String expectedText,String attemptsText){
        View row=LayoutInflater.from(this).inflate(R.layout.target_row,targets,false);Spinner kind=row.findViewById(R.id.kind);EditText address=row.findViewById(R.id.address),expected=row.findViewById(R.id.expected_status),attempts=row.findViewById(R.id.attempts);
        ArrayAdapter<String> adapter=new ArrayAdapter<>(this,android.R.layout.simple_spinner_item,kindLabels());adapter.setDropDownViewResource(android.R.layout.simple_spinner_dropdown_item);kind.setAdapter(adapter);kind.setSaveEnabled(false);kind.setSelection(kindIndex(initial));
        address.setSaveEnabled(false);expected.setSaveEnabled(false);attempts.setSaveEnabled(false);address.setText(addressText);expected.setText(expectedText);attempts.setText(attemptsText);
        TextWatcher addressWatcher=addressWatcher(),watcher=watcher();address.addTextChangedListener(addressWatcher);expected.addTextChangedListener(watcher);attempts.addTextChangedListener(watcher);
        boolean[] initialSelection={true};
        AdapterView.OnItemSelectedListener kindListener=new AdapterView.OnItemSelectedListener(){public void onNothingSelected(AdapterView<?> p){}public void onItemSelected(AdapterView<?> p,View v,int position,long id){updateRow(row,CheckKind.values()[position]);if(initialSelection[0]){initialSelection[0]=false;return;}inputMutated();}};
        kind.setOnItemSelectedListener(kindListener);
        Button remove=row.findViewById(R.id.remove);row.setTag(new TargetRowListeners(addressWatcher,watcher));
        remove.setOnClickListener(v->{if(targets.getChildCount()>1){detachTargetRowListeners(row);targets.removeView(row);refreshRemoveDescriptions();inputMutated();}});
        targets.addView(row);updateRow(row,initial);refreshRemoveDescriptions();addTarget.setEnabled(targets.getChildCount()<MAX_TARGETS);
    }
    private void detachTargetRowListeners(View row){
        Object tag=row.getTag();if(!(tag instanceof TargetRowListeners listeners))return;
        ((EditText)row.findViewById(R.id.address)).removeTextChangedListener(listeners.address());
        ((EditText)row.findViewById(R.id.expected_status)).removeTextChangedListener(listeners.fields());
        ((EditText)row.findViewById(R.id.attempts)).removeTextChangedListener(listeners.fields());
        ((Spinner)row.findViewById(R.id.kind)).setOnItemSelectedListener(null);
        row.findViewById(R.id.remove).setOnClickListener(null);row.setTag(null);
    }
    private void updateRow(View row,CheckKind kind){
        EditText address=row.findViewById(R.id.address),expected=row.findViewById(R.id.expected_status),attempts=row.findViewById(R.id.attempts);address.setHint(addressHint(kind));
        int http=kind.supportsExpectedStatus()?View.VISIBLE:View.GONE;expected.setVisibility(http);row.findViewById(R.id.expected_status_label).setVisibility(http);
        int trace=kind==CheckKind.TRACEROUTE?View.VISIBLE:View.GONE;attempts.setVisibility(trace);row.findViewById(R.id.attempts_label).setVisibility(trace);refreshRemoveDescriptions();
    }
    private void refreshRemoveDescriptions(){for(int i=0;i<targets.getChildCount();i++){View row=targets.getChildAt(i);Spinner spinner=row.findViewById(R.id.kind);EditText address=row.findViewById(R.id.address);String kind=spinner.getSelectedItem()==null?"":spinner.getSelectedItem().toString(),presentation=targetAddressPresentation(address.getText());row.findViewById(R.id.remove).setContentDescription(presentation.isEmpty()?getString(R.string.remove_target_without_address,i+1,kind):getString(R.string.remove_target_with_address,i+1,kind,presentation));}}
    private static String targetAddressPresentation(CharSequence raw){
        String value=raw==null?"":raw.toString();
        int scheme=value.indexOf("://");
        if(scheme<0){
            StringBuilder structural=new StringBuilder(value.length());
            for(int offset=0;offset<value.length();){int codePoint=value.codePointAt(offset);offset+=Character.charCount(codePoint);if(!isAddressPresentationHazard(codePoint))structural.appendCodePoint(codePoint);}
            if(structural.indexOf("://")>=0)return "";
        }else{
            int schemeStart=scheme;
            while(schemeStart>0){int codePoint=value.codePointBefore(schemeStart);if(!isUriSchemeCodePoint(codePoint)&&!isAddressPresentationHazard(codePoint))break;schemeStart-=Character.charCount(codePoint);}
            while(schemeStart<scheme){int codePoint=value.codePointAt(schemeStart);if(!isAddressPresentationHazard(codePoint))break;schemeStart+=Character.charCount(codePoint);}
            for(int offset=schemeStart;offset<scheme;){int codePoint=value.codePointAt(offset);if(isAddressPresentationHazard(codePoint))return "";offset+=Character.charCount(codePoint);}
            int authorityEnd=value.length();
            for(char separator:new char[]{'/','?','#'}){int found=value.indexOf(separator,scheme+3);if(found>=0&&found<authorityEnd)authorityEnd=found;}
            for(int offset=scheme+3;offset<authorityEnd;){int codePoint=value.codePointAt(offset);if(isAddressPresentationHazard(codePoint))return "";offset+=Character.charCount(codePoint);}
            int at=value.lastIndexOf('@',authorityEnd-1);
            if(at>=scheme+3)value=value.substring(0,scheme+3)+value.substring(at+1);
        }
        int suffix=value.length();for(char separator:new char[]{'?','#'}){int found=value.indexOf(separator);if(found>=0&&found<suffix)suffix=found;}value=value.substring(0,suffix).strip();
        StringBuilder clean=new StringBuilder(value.length());
        boolean previousSpace=false;
        for(int offset=0;offset<value.length();){int codePoint=value.codePointAt(offset);offset+=Character.charCount(codePoint);boolean unsafe=isAddressPresentationHazard(codePoint)||Character.isWhitespace(codePoint);if(unsafe){if(!previousSpace)clean.append(' ');previousSpace=true;}else{clean.appendCodePoint(codePoint);previousSpace=false;}}
        value=clean.toString().strip();
        int count=value.codePointCount(0,value.length());
        if(count>MAX_REMOVE_ADDRESS_CODE_POINTS){int end=value.offsetByCodePoints(0,MAX_REMOVE_ADDRESS_CODE_POINTS-1);value=value.substring(0,end)+"…";}
        return value;
    }
    private static boolean isAddressPresentationHazard(int codePoint){return Character.isISOControl(codePoint)||Character.getType(codePoint)==Character.FORMAT;}
    private static boolean isUriSchemeCodePoint(int codePoint){return codePoint>='a'&&codePoint<='z'||codePoint>='A'&&codePoint<='Z'||codePoint>='0'&&codePoint<='9'||codePoint=='+'||codePoint=='-'||codePoint=='.';}
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
            case READY->renderReady(state);
            case ERROR->{status.setText(R.string.state_error);clearReady();showTransportError(state.error().orElseThrow());}
            case CANCELLED->{status.setText(R.string.state_cancelled);clearReady();status.requestFocus();}
        }
    }
    private void showTransportError(TransportException failure){
        if(failure.kind()==TransportException.Kind.API){showApiError(failure.apiError().orElseThrow());return;}
        int res=switch(failure.kind()){
            case NETWORK->R.string.error_network;
            case TIMEOUT->R.string.error_timeout_request;
            case RESPONSE_TOO_LARGE->R.string.error_response_large;
            case INVALID_RESPONSE->R.string.error_invalid_response;
            case UNSUPPORTED_CAPABILITY->capabilityErrorResource(
                    failure.capabilityMismatchReason().orElseThrow());
            case API,CANCELLED->throw new IllegalStateException("Unexpected error-state failure kind");
        };showError(getString(res));
    }
    private void showApiError(ApiError api){
        ApiErrorPresentation.Entry presentation=ApiErrorPresentation.resolve(api);
        retry.setVisibility(presentation.retryable()?View.VISIBLE:View.GONE);
        String message=getString(presentation.messageResource());
        if(presentation.retryAfterAllowed()&&api.retryAt().isPresent())
            message+="\n"+getString(R.string.retry_after,api.retryAt().orElseThrow().toString());
        error.setText(message);ViewCompat.setStateDescription(error,getString(R.string.accessibility_error_state));error.setVisibility(View.VISIBLE);
        if(presentation.credentialFocus()&&bearer.getVisibility()==View.VISIBLE)bearer.requestFocus();
        else error.requestFocus();
    }
    private static int capabilityErrorResource(CheckCapabilities.CapabilityMismatchException.Reason reason){
        return switch(reason){
            case CHECK_KIND->R.string.error_capability_check_kind;
            case TARGET_COUNT->R.string.error_capability_target_count;
            case TIMEOUT->R.string.error_capability_timeout;
            case TRACEROUTE_ATTEMPTS->R.string.error_capability_traceroute_attempts;
            case TOPOLOGY_MODE->R.string.error_capability_topology_mode;
        };
    }
    private void showError(String message){error.setText(message);ViewCompat.setStateDescription(error,getString(R.string.accessibility_error_state));error.setVisibility(View.VISIBLE);error.requestFocus();}
    private void hideError(){error.setVisibility(View.GONE);ViewCompat.setStateDescription(error,null);}
    private void clearReady(){presentationGeneration++;presentationPhase=PresentationPhase.EMPTY;forgetInstalledCandidate();readyReport=null;readyRaw=null;pendingRawShare=null;if(rawWarningDialog!=null){rawWarningDialog.dismiss();rawWarningDialog=null;}retireRawShareAndCancelPreparation();results.removeAllViews();report.setVisibility(View.GONE);share.setEnabled(false);shareRaw.setEnabled(false);}
    private void renderReady(RequestState state){
        final long generation=++presentationGeneration;
        final PresentationOwner expected=new PresentationOwner(state.ownerId(),state.signature(),state.rawJson().orElseThrow(),state.report().orElseThrow());
        presentationPhase=PresentationPhase.RENDERING;
        final View candidate;
        try{
            AnalysisPresentation presentation=AnalysisPresentation.from(expected.report());
            candidate=presentationRenderer.render(this,presentation,point->{});
            if(candidate==null||candidate.getParent()!=null)throw new IllegalStateException("renderer must return one detached hierarchy");
            presentationCommitter.checkpoint(CommitPoint.PRE_SWAP);
        }catch(RuntimeException failure){failPresentationIfCurrent(generation,expected,null);return;}
        if(!isCurrentReady(generation,expected)){discardCandidate(candidate);recoverCurrentInstalledHierarchy();return;}
        try{
            presentationCommitter.swap(results,candidate);
        }catch(RuntimeException failure){failPresentationIfCurrent(generation,expected,candidate);return;}
        if(!isCurrentReady(generation,expected)){discardCandidate(candidate);recoverCurrentInstalledHierarchy();return;}
        if(!isExactInstalledHierarchy(candidate)){rollbackCandidateIfCurrent(generation,expected,candidate,true);return;}
        installedCandidate=candidate;installedCandidateGeneration=generation;installedCandidateOwner=expected;
        try{
            publishStep(generation,expected,CommitPoint.REPORT_VISIBILITY,()->report.setVisibility(View.VISIBLE));
            publishStep(generation,expected,CommitPoint.READY_STATUS,()->status.setText(R.string.state_ready));
            publishStep(generation,expected,CommitPoint.FOCUS,()->report.requestFocus());
            publishStep(generation,expected,CommitPoint.LIVE_ANNOUNCEMENT,()->status.announceForAccessibility(getString(R.string.state_ready)));
            publishStep(generation,expected,CommitPoint.SHARE_ELIGIBILITY,()->{
                readyReport=expected.report();
                readyRaw=new ReadyRaw(expected.ownerId(),expected.signature(),expected.raw());
                share.setEnabled(true);shareRaw.setEnabled(true);
            });
            if(!isCurrentReady(generation,expected))throw new StalePresentationException();
            hideError();presentationPhase=PresentationPhase.RENDERED;
        }catch(StalePresentationException stale){discardCandidate(candidate);recoverCurrentInstalledHierarchy();}
        catch(RuntimeException failure){rollbackCandidateIfCurrent(generation,expected,candidate,true);}
    }
    private static View buildDetachedPresentation(MainActivity activity,AnalysisPresentation value,RenderCheckpoint checkpoint){
        checkpoint.at(RenderPoint.BEFORE_FIRST_VIEW);
        LinearLayout candidate=new LinearLayout(activity);candidate.setOrientation(LinearLayout.VERTICAL);
        int index=0;
        for(AnalysisPresentation.Block block:value.blocks()){
            if(index++==1)checkpoint.at(RenderPoint.MID_BUILD);
            if(block.folded()){activity.addFoldedBlock(candidate,block);continue;}
            TextView heading=new TextView(activity);heading.setText(block.heading());heading.setTextColor(activity.getColor(R.color.lime));heading.setTextSize(18);heading.setTypeface(null,android.graphics.Typeface.BOLD);ViewCompat.setAccessibilityHeading(heading,true);heading.setPadding(0,12,0,4);candidate.addView(heading);
            TextView body=new TextView(activity);body.setText(block.body());body.setTextColor(activity.getColor(R.color.ink));body.setTextSize(15);body.setPadding(0,0,0,12);candidate.addView(body);
        }
        return candidate;
    }
    private void addFoldedBlock(LinearLayout candidate,AnalysisPresentation.Block block){
        Button toggle=new Button(this);toggle.setId(R.id.raw_results_toggle);toggle.setText(R.string.raw_results_show);toggle.setAllCaps(false);toggle.setMinHeight(dp(48));toggle.setPadding(dp(12),dp(12),dp(12),dp(12));
        TextView body=new TextView(this);body.setId(R.id.raw_results_body);body.setText(block.body());body.setTextColor(getColor(R.color.ink));body.setTextSize(15);body.setPadding(0,dp(8),0,dp(12));body.setVisibility(View.GONE);
        ViewCompat.setStateDescription(toggle,getString(R.string.raw_results_collapsed));
        toggle.setOnClickListener(v->{boolean expand=body.getVisibility()!=View.VISIBLE;body.setVisibility(expand?View.VISIBLE:View.GONE);toggle.setText(expand?R.string.raw_results_hide:R.string.raw_results_show);ViewCompat.setStateDescription(toggle,getString(expand?R.string.raw_results_expanded:R.string.raw_results_collapsed));});
        candidate.addView(toggle,new LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT,LinearLayout.LayoutParams.WRAP_CONTENT));candidate.addView(body);
    }
    private void publishStep(long generation,PresentationOwner expected,CommitPoint point,Runnable publication){if(!isCurrentReady(generation,expected))throw new StalePresentationException();presentationCommitter.checkpoint(point);if(!isCurrentReady(generation,expected))throw new StalePresentationException();publication.run();}
    private boolean isCurrentReady(long generation,PresentationOwner expected){if(generation!=presentationGeneration)return false;RequestState current=session.state();return current.phase()==RequestState.Phase.READY&&current.ownerId()==expected.ownerId()&&current.signature().equals(expected.signature())&&current.rawJson().filter(expected.raw()::equals).isPresent()&&current.report().filter(value->value==expected.report()).isPresent();}
    private void failPresentationIfCurrent(long generation,PresentationOwner expected,View candidate){if(!isCurrentReady(generation,expected)){discardCandidate(candidate);recoverCurrentInstalledHierarchy();return;}rollbackCandidateIfCurrent(generation,expected,candidate,true);}
    private boolean isExactInstalledHierarchy(View candidate){return candidate.getParent()==results&&results.getChildCount()==1&&results.getChildAt(0)==candidate;}
    private void forgetInstalledCandidate(){installedCandidate=null;installedCandidateGeneration=-1;installedCandidateOwner=null;}
    private void recoverCurrentInstalledHierarchy(){
        View current=installedCandidate;PresentationOwner expected=installedCandidateOwner;long generation=installedCandidateGeneration;
        if(current==null||expected==null||!isCurrentReady(generation,expected))return;
        if(current.getParent()!=null&&current.getParent()!=results)return;
        try{
            if(current.getParent()==null)results.addView(current);
            for(int index=results.getChildCount()-1;index>=0;index--)if(results.getChildAt(index)!=current)results.removeViewAt(index);
        }catch(RuntimeException ignored){}
    }
    private void rollbackCandidateIfCurrent(long generation,PresentationOwner expected,View candidate,boolean fixedError){
        if(!isCurrentReady(generation,expected)||installedCandidate!=null&&(installedCandidate!=candidate||installedCandidateGeneration!=generation||installedCandidateOwner!=expected)){discardCandidate(candidate);recoverCurrentInstalledHierarchy();return;}
        try{presentationCommitter.rollback(results);}catch(RuntimeException ignored){try{results.removeAllViews();}catch(RuntimeException ignoredAgain){}}
        if(!isCurrentReady(generation,expected)){discardCandidate(candidate);recoverCurrentInstalledHierarchy();return;}
        forgetInstalledCandidate();readyReport=null;readyRaw=null;
        try{share.setEnabled(false);}catch(RuntimeException ignored){}try{shareRaw.setEnabled(false);}catch(RuntimeException ignored){}
        try{report.setVisibility(View.GONE);}catch(RuntimeException ignored){}try{report.clearFocus();}catch(RuntimeException ignored){}
        if(fixedError){presentationPhase=PresentationPhase.RENDER_ERROR;try{retry.setVisibility(View.VISIBLE);}catch(RuntimeException ignored){}try{status.setText(R.string.state_render_error);}catch(RuntimeException ignored){}try{error.setText(R.string.error_render_report);}catch(RuntimeException ignored){}try{error.setVisibility(View.VISIBLE);}catch(RuntimeException ignored){}try{ViewCompat.setStateDescription(error,getString(R.string.accessibility_error_state));}catch(RuntimeException ignored){}try{error.requestFocus();}catch(RuntimeException ignored){}}
        else presentationPhase=PresentationPhase.EMPTY;
    }
    private static void discardCandidate(View candidate){if(candidate!=null&&candidate.getParent() instanceof android.view.ViewGroup parent)try{parent.removeView(candidate);}catch(RuntimeException ignored){}}
    private int dp(int value){return Math.round(value*getResources().getDisplayMetrics().density);}
    PresentationPhase presentationPhaseForTests(){return presentationPhase;}
    void renderReadyForTest(String ignoredRaw,Report value){View candidate=DEFAULT_PRESENTATION_RENDERER.render(this,AnalysisPresentation.from(value),point->{});results.removeAllViews();results.addView(candidate);readyReport=value;readyRaw=null;report.setVisibility(View.VISIBLE);share.setEnabled(true);}
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
        PendingRawShare request=new PendingRawShare(candidate.ownerId(),candidate.signature(),candidate.json());
        RawShareRuntime.Admission admission=rawRuntime.prepare(retained.rawOwner,candidate.json());
        if(admission==RawShareRuntime.Admission.ACCEPTED){pendingRawShare=request;shareRaw.setEnabled(false);status.setText(R.string.raw_share_state_cleaning);return;}
        if(admission==RawShareRuntime.Admission.BUSY){status.setText(R.string.raw_share_busy);return;}
        showRawShareFailure();
    }

    private void renderRawShareState(RawShareRuntime.Snapshot snapshot){
        if(!rawAttached||isFinishing()||isDestroyed())return;
        switch(snapshot.phase()){
            case CLEANING->{if(pendingRawShare!=null)status.setText(R.string.raw_share_state_cleaning);}
            case PREPARING->{if(pendingRawShare!=null)status.setText(R.string.raw_share_state_preparing);}
            case MATERIALIZED->{status.setText(R.string.raw_share_state_materialized);launchMaterializedRaw(snapshot);}
            case GRANTED->status.setText(R.string.raw_share_state_granted);
            case RETIRED->status.setText(R.string.raw_share_state_retired);
            case DISPOSED->status.setText(R.string.raw_share_state_disposed);
            case CLEANUP_FAILED->showRawShareFailure();
            case IDLE->{
                boolean failed=pendingRawShare!=null&&snapshot.failure()!=null;
                pendingRawShare=null;
                if(failed)showRawShareFailure();
                else if(session.state().phase()==RequestState.Phase.READY)status.setText(R.string.state_ready);
            }
        }
    }

    private void launchMaterializedRaw(RawShareRuntime.Snapshot notified){
        PendingRawShare request=pendingRawShare;if(request==null){rawRuntime.retire(retained.rawOwner);return;}
        RequestState current=session.state();RawShareRuntime.Snapshot exact=rawRuntime.snapshot();
        if(!rawAttached||current.phase()!=RequestState.Phase.READY||current.ownerId()!=request.ownerId()||!current.signature().equals(request.signature())||!current.rawJson().filter(request.json()::equals).isPresent()||exact.phase()!=RawShareRuntime.Phase.MATERIALIZED||exact.uri()==null||!exact.uri().equals(notified.uri())){
            pendingRawShare=null;rawRuntime.retire(retained.rawOwner);return;
        }
        Uri stream=rawRuntime.grant(retained.rawOwner);RawShareRuntime.Snapshot granted=rawRuntime.snapshot();
        if(stream==null||granted.phase()!=RawShareRuntime.Phase.GRANTED||!stream.equals(granted.uri())){pendingRawShare=null;rawRuntime.retire(retained.rawOwner);showRawShareFailure();return;}
        try{
            Intent send=new Intent(Intent.ACTION_SEND).setType("application/json").putExtra(Intent.EXTRA_STREAM,stream).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
            chooserLauncher.launch(this,Intent.createChooser(send,getString(R.string.raw_share_chooser)));
            pendingRawShare=null;status.setText(R.string.raw_share_state_granted);
        }catch(ActivityNotFoundException|SecurityException failure){pendingRawShare=null;rawRuntime.retire(retained.rawOwner);showRawShareFailure();}
        catch(RuntimeException failure){pendingRawShare=null;rawRuntime.retire(retained.rawOwner);showRawShareFailure();}
    }

    private void retireRawShareAndCancelPreparation(){
        if(rawRuntime==null)return;
        rawRuntime.retire(retained.rawOwner);
        if(rawAttached){detachRawRuntime();attachRawRuntime();}
    }
    private void showRawShareFailure(){showError(getString(R.string.raw_share_error));}

    @Override protected void onSaveInstanceState(Bundle out){super.onSaveInstanceState(out);try{FormState form=collectForm();out.putString(KEY_API,bounded(form.apiBase()));out.putInt(KEY_TIMEOUT,form.timeoutMs());ArrayList<String> kinds=new ArrayList<>(),addresses=new ArrayList<>(),expected=new ArrayList<>(),attempts=new ArrayList<>();for(FormState.Target target:form.targets()){kinds.add(target.kind().wireValue());addresses.add(bounded(target.address()));expected.add(bounded(target.expectedStatus()));attempts.add(bounded(target.attempts()));}out.putStringArrayList(KEY_KINDS,kinds);out.putStringArrayList(KEY_ADDRESSES,addresses);out.putStringArrayList(KEY_EXPECTED,expected);out.putStringArrayList(KEY_ATTEMPTS,attempts);}catch(RuntimeException ignored){}}
    @Override public Object onRetainNonConfigurationInstance(){retainingSession=true;retained.bearer=bearer.getText().toString();retained.authEnabled=authEnabled.isChecked();return retained;}
    @Override protected void onDestroy(){
        for(int i=0;i<targets.getChildCount();i++)detachTargetRowListeners(targets.getChildAt(i));
        session.detach(owner);
        boolean finalDestroy=!retainingSession&&!isChangingConfigurations();
        if(finalDestroy){pendingRawShare=null;rawRuntime.retire(retained.rawOwner);session.destroy();}
        detachRawRuntime();
        super.onDestroy();
    }
    private record ReadyRaw(long ownerId,String signature,String json){}
    private record PendingRawShare(long ownerId,String signature,String json){}
    private record PresentationOwner(long ownerId,String signature,String raw,Report report){}
    private record TargetRowListeners(TextWatcher address,TextWatcher fields){}
    private static final class StalePresentationException extends RuntimeException {}
    private static final class Retained {final DiagnosticsSession session;final Object rawOwner=new Object();String bearer="";boolean authEnabled;Retained(DiagnosticsSession session){this.session=session;}}
}
