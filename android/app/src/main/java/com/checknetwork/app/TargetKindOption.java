package com.checknetwork.app;

import com.checknetwork.app.core.CheckKind;
import java.util.Arrays;
import java.util.Collections;
import java.util.List;

/** Context-rich immutable selector item; wire values never double as user-facing labels. */
public final class TargetKindOption {
    private final CheckKind kind;
    private final String label;
    private final String hint;

    private TargetKindOption(CheckKind kind, String label, String hint) {
        this.kind=kind; this.label=label; this.hint=hint;
    }
    public CheckKind kind(){return kind;} public String label(){return label;} public String addressHint(){return hint;}
    @Override public String toString(){return label;}

    private static final List<TargetKindOption> ALL=Collections.unmodifiableList(Arrays.asList(
        new TargetKindOption(CheckKind.DNS,"DNS lookup","Hostname, e.g. example.com"),
        new TargetKindOption(CheckKind.TCP,"TCP connection","Host:port, e.g. 1.1.1.1:443"),
        new TargetKindOption(CheckKind.HTTP,"HTTP request","http:// URL"),
        new TargetKindOption(CheckKind.HTTPS,"HTTPS request","https:// URL"),
        new TargetKindOption(CheckKind.SSH,"SSH service","Host or host:port"),
        new TargetKindOption(CheckKind.SMTP,"SMTP service","Mail host or host:port"),
        new TargetKindOption(CheckKind.SUBMISSION,"Mail submission","Mail host or host:port"),
        new TargetKindOption(CheckKind.SMTPS,"Secure SMTP","Mail host or host:port"),
        new TargetKindOption(CheckKind.IMAP,"IMAP service","Mail host or host:port"),
        new TargetKindOption(CheckKind.IMAPS,"Secure IMAP","Mail host or host:port"),
        new TargetKindOption(CheckKind.POP3,"POP3 service","Mail host or host:port"),
        new TargetKindOption(CheckKind.POP3S,"Secure POP3","Mail host or host:port"),
        new TargetKindOption(CheckKind.TRACEROUTE,"Traceroute path","Hostname or IP address")
    ));
    public static List<TargetKindOption> all(){return ALL;}
    public static TargetKindOption forKind(CheckKind kind){
        for(TargetKindOption option:ALL) if(option.kind==kind)return option;
        throw new IllegalArgumentException("unsupported kind");
    }
}
