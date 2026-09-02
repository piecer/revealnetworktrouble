package com.checknetwork.app;

import com.checknetwork.app.core.CheckKind;
import com.checknetwork.app.core.ContractLimits;
import com.checknetwork.app.core.ReportRequest;
import com.checknetwork.app.core.TargetInput;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

/** Bounded immutable non-secret form snapshot suitable for instance-state persistence. */
public final class FormState {
    private final String apiBase;
    private final int timeoutMs;
    private final List<Target> targets;
    public FormState(String apiBase,int timeoutMs,List<Target> targets){
        this.apiBase=Objects.requireNonNull(apiBase,"apiBase");
        if(apiBase.length()>ContractLimits.MAX_STRING_CHARS)throw new IllegalArgumentException("API base is too long");
        if(timeoutMs<ContractLimits.MIN_TIMEOUT_MS||timeoutMs>ContractLimits.MAX_TIMEOUT_MS)throw new IllegalArgumentException("Timeout must be 100 to 30000 ms");
        if(targets==null||targets.isEmpty()||targets.size()>ContractLimits.MAX_TARGETS)throw new IllegalArgumentException("Provide 1 to 20 targets");
        this.timeoutMs=timeoutMs; this.targets=Collections.unmodifiableList(new ArrayList<>(targets));
    }
    public String apiBase(){return apiBase;} public int timeoutMs(){return timeoutMs;} public List<Target> targets(){return targets;}
    public ReportRequest toRequest(){
        boolean topologyOnly=true;
        for(Target target:targets)if(target.kind()!=CheckKind.TRACEROUTE)topologyOnly=false;
        ReportRequest.Builder builder=topologyOnly?ReportRequest.topologyBuilder():ReportRequest.builder();
        builder.timeoutMs(timeoutMs);
        for(Target target:targets)builder.addTarget(target.toInput());
        return builder.build();
    }
    public String signature(){return apiBase+'|'+toRequest().signature();}

    public static final class Target {
        private final CheckKind kind; private final String address,expectedStatus,attempts;
        public Target(CheckKind kind,String address,String expectedStatus,String attempts){
            this.kind=Objects.requireNonNull(kind,"kind"); this.address=Objects.requireNonNull(address,"address");
            this.expectedStatus=expectedStatus==null?"":expectedStatus.trim(); this.attempts=attempts==null?"":attempts.trim();
            if(address.length()>ContractLimits.MAX_STRING_CHARS)throw new IllegalArgumentException("Address is too long");
        }
        public CheckKind kind(){return kind;} public String address(){return address;} public String expectedStatus(){return expectedStatus;} public String attempts(){return attempts;}
        TargetInput toInput(){
            TargetInput.Builder builder=TargetInput.builder(kind,address);
            if(!expectedStatus.isEmpty()){
                if(!kind.supportsExpectedStatus())throw new IllegalArgumentException("Expected status applies only to HTTP and HTTPS");
                builder.expectedStatus(parse(expectedStatus,"Expected status must be 100 to 599"));
            }
            if(!attempts.isEmpty()){
                if(kind!=CheckKind.TRACEROUTE)throw new IllegalArgumentException("Attempts applies only to traceroute");
                builder.attempts(parse(attempts,"Attempts must be 1 to 10"));
            }
            return builder.build();
        }
        private static int parse(String text,String message){try{return Integer.parseInt(text);}catch(NumberFormatException e){throw new IllegalArgumentException(message);}}
    }
}
