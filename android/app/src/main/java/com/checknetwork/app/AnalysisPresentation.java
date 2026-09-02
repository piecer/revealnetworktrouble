package com.checknetwork.app;

import com.checknetwork.app.core.Report;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/** Native-text presentation contract in deliberate diagnostic reading order. */
public final class AnalysisPresentation {
    public static final int MAX_RAW_RESULT_CHARS=256*1024;
    public static final class Block {
        private final String heading,body;private final boolean folded;
        Block(String heading,String body){this(heading,body,false);}
        Block(String heading,String body,boolean folded){this.heading=heading;this.body=body;this.folded=folded;}
        public String heading(){return heading;} public String body(){return body;}
        public boolean folded(){return folded;}
        @Override public String toString(){return heading+": "+body;}
    }
    private final List<Block> blocks;
    private AnalysisPresentation(List<Block> blocks){this.blocks=Collections.unmodifiableList(blocks);}
    public List<Block> blocks(){return blocks;}

    public static AnalysisPresentation from(Report report){
        List<Block> out=new ArrayList<>();
        StringBuilder identity=new StringBuilder("ID: ").append(report.id())
                .append("\nStatus: ").append(status(report.status()))
                .append("\nStarted: ").append(report.startedAt())
                .append("\nDuration: ").append(report.durationMs()).append(" ms")
                .append("\nCoverage: ");
        if(report.analysis().isPresent()){
            Report.Analysis a=report.analysis().orElseThrow();
            identity.append(a.coverage().available().size()).append(" available, ")
                    .append(a.coverage().missing().size()).append(" missing; verdict ").append(verdict(a.verdict()));
        } else identity.append("analysis unavailable");
        out.add(new Block("Report",identity.toString()));
        if(report.analysis().isPresent()){
            Report.Analysis a=report.analysis().orElseThrow();
            StringBuilder findings=new StringBuilder();
            for(Report.Finding f:a.findings())findings.append(f.title()).append("\n").append(f.summary())
                    .append("\nConfidence: ").append(title(f.confidence().name())).append("\n");
            if(findings.length()==0)findings.append("No findings");
            out.add(new Block("Findings",findings.toString().trim()));
            StringBuilder evidence=new StringBuilder();
            for(Report.Evidence e:a.evidence())evidence.append(e.kind().wireValue().toUpperCase(Locale.ROOT)).append(" — ")
                    .append(e.signal()).append(": ").append(e.observed()).append("; expected ").append(e.expected()).append('\n');
            out.add(new Block("Evidence",evidence.length()==0?"No evidence records":evidence.toString().trim()));
            StringBuilder actions=new StringBuilder();
            for(Report.Action action:a.actions())actions.append(action.title()).append("\nStep: ").append(action.step())
                    .append("\nExpected: ").append(action.expectedResult()).append("\nEscalate when: ").append(action.escalationCondition()).append('\n');
            out.add(new Block("Actions",actions.length()==0?"No recommended actions":actions.toString().trim()));
            Report.Coverage c=a.coverage();
            StringBuilder coverage=new StringBuilder("Available: ").append(c.available().size()).append("\nMissing: ").append(c.missing().size())
                    .append("\nProvider failures: ").append(c.providerFailures().size()).append("\nLimitations: ").append(c.limitations().size());
            for(Report.CoverageIssue issue:c.providerFailures())coverage.append("\nProvider failure — ").append(issue.signal()).append(": ").append(issue.reason());
            for(Report.CoverageIssue issue:c.limitations())coverage.append("\nLimitation — ").append(issue.signal()).append(": ").append(issue.reason());
            out.add(new Block("Coverage and limitations",coverage.toString()));
        } else {
            out.add(new Block("Findings","Analysis unavailable (legacy report)"));
        }
        StringBuilder results=new StringBuilder("Checks: ").append(report.summary().total()).append(" total, ")
                .append(report.summary().passed()).append(" passed, ").append(report.summary().failed()).append(" failed");
        for(Report.Result result:report.results())results.append("\n").append(result.kind().wireValue().toUpperCase(Locale.ROOT)).append(" — ")
                .append(status(result.status())).append(" — ").append(result.latencyMs()).append(" ms");
        out.add(new Block("Result summary",results.toString()));
        report.compactTopology().ifPresent(t->out.add(new Block("Compact topology","Nodes: "+t.nodeCount()+"\nLinks: "+t.linkCount()+"\nRoutes: "+t.routeCount()+"\nTruncated: "+(t.truncated()?"yes":"no"))));
        out.add(new Block("Raw results",rawResults(report),true));
        return new AnalysisPresentation(out);
    }

    private static String rawResults(Report report){
        BoundedText out=new BoundedText(MAX_RAW_RESULT_CHARS);
        int index=0;
        for(Report.Result result:report.results()){
            if(index>0)out.add("\n\n");
            out.add("Result ").add(String.valueOf(++index))
                    .add("\nKind: ").add(result.kind().wireValue().toUpperCase(Locale.ROOT))
                    .add("\nStatus: ").add(status(result.status()))
                    .add("\nAddress: ").add(result.address())
                    .add("\nLatency: ").add(String.valueOf(result.latencyMs())).add(" ms")
                    .add("\nError: ").add(emptyAsNone(result.errorCode()))
                    .add("\nMessage: ").add(emptyAsNone(result.message()))
                    .add("\nDetails:");
            if(result.details().isEmpty())out.add(" none");
            else for(Map.Entry<String,Object> detail:result.details().entrySet())out.add("\n  ").add(detail.getKey()).add(": ").addValue(detail.getValue());
        }
        if(report.results().isEmpty())out.add("No result records");
        report.compactTopology().ifPresent(topology->{
            out.add("\n\nCompact topology: ").add(String.valueOf(topology.nodeCount())).add(" nodes, ")
                    .add(String.valueOf(topology.linkCount())).add(" links, ").add(String.valueOf(topology.routeCount()))
                    .add(" routes; truncated: ").add(topology.truncated()?"yes":"no");
            Object geo=topology.opaqueData().get("geo");
            out.add("\nGeo observations: ");if(geo instanceof Map<?,?>)out.addValue(geo);else out.add("none");
        });
        return out.text();
    }
    private static String emptyAsNone(String value){return value==null||value.isEmpty()?"none":value;}
    private static final class BoundedText{
        private final int limit;private final StringBuilder value=new StringBuilder();private boolean truncated;
        BoundedText(int limit){this.limit=limit;}
        BoundedText add(String text){if(truncated||text==null)return this;int remaining=limit-value.length();if(text.length()<=remaining)value.append(text);else{if(remaining>1)value.append(text,0,remaining-1);if(remaining>0)value.append('…');truncated=true;}return this;}
        BoundedText addValue(Object item){
            if(item==null)return add("null");
            if(item instanceof Map<?,?> map){add("{");boolean first=true;for(Map.Entry<?,?> entry:map.entrySet()){if(!first)add(", ");first=false;add(String.valueOf(entry.getKey())).add(": ").addValue(entry.getValue());}return add("}");}
            if(item instanceof List<?> list){add("[");for(int i=0;i<list.size();i++){if(i>0)add(", ");addValue(list.get(i));}return add("]");}
            return add(String.valueOf(item));
        }
        String text(){return value.toString();}
    }
    private static String status(Report.Status s){return switch(s){case HEALTHY->"Healthy";case DEGRADED->"Degraded";case UNREACHABLE->"Unreachable";};}
    private static String verdict(Report.Verdict v){return switch(v){case HEALTHY->"Healthy";case ATTENTION->"Attention";case INCONCLUSIVE->"Inconclusive";};}
    private static String title(String value){String lower=value.toLowerCase(Locale.ROOT);return Character.toUpperCase(lower.charAt(0))+lower.substring(1);}
}
