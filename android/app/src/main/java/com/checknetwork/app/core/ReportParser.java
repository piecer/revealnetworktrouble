package com.checknetwork.app.core;

import java.math.BigDecimal;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;
import org.json.JSONTokener;

/** Strict parser from untrusted report JSON into immutable Java values. */
public final class ReportParser {
    private ReportParser() {}

    public static Report parse(String json) {
        if (json == null) throw error("report", "expected JSON object");
        preflight(json);
        try {
            JSONTokener tokener = new JSONTokener(json);
            Object value = tokener.nextValue();
            if (!(value instanceof JSONObject)) throw error("report", "expected one JSON object");
            if (tokener.nextClean() != 0) throw error("report", "must contain one JSON object");
            JSONObject source = (JSONObject) value;
            enforceAggregateBudget(source);
            return parseReport(source);
        } catch (ReportParseException exception) {
            throw exception;
        } catch (JSONException exception) {
            throw new ReportParseException("invalid report JSON", exception);
        }
    }

    private static void preflight(String json) {
        if (json.getBytes(StandardCharsets.UTF_8).length > ContractLimits.MAX_TRANSPORT_BYTES) {
            throw error("report", "UTF-8 body exceeds byte limit");
        }
        int depth = 0;
        boolean inString = false;
        boolean escaped = false;
        for (int i = 0; i < json.length(); i++) {
            char current = json.charAt(i);
            if (inString) {
                if (escaped) escaped = false;
                else if (current == '\\') escaped = true;
                else if (current == '"') inString = false;
                continue;
            }
            if (current == '"') {
                inString = true;
            } else if (current == '{' || current == '[') {
                if (++depth > ContractLimits.MAX_JSON_DEPTH) {
                    throw error("report", "nested too deeply");
                }
            } else if (current == '}' || current == ']') {
                depth--;
            }
        }
    }

    private static Report parseReport(JSONObject source) throws JSONException {
        String id = text(source, "id", "report.id", true, ContractLimits.MAX_STRING_CHARS);
        Report.Status status = enumValue(text(source,"status","report.status",true,64), Report.Status.class, "report.status");
        Instant startedAt = timestamp(source, "started_at", "report.started_at");
        long duration = integer(source, "duration_ms", "report.duration_ms", 0, Long.MAX_VALUE);
        JSONArray rawResults = array(source, "results", "report.results", ContractLimits.MAX_TARGETS);
        List<Report.Result> results = new ArrayList<>();
        for (int i=0;i<rawResults.length();i++) results.add(parseResult(object(rawResults.get(i), "report.results["+i+"]"), i));
        JSONObject rawSummary = object(required(source,"summary","report.summary"), "report.summary");
        int total=(int)integer(rawSummary,"total","report.summary.total",0,ContractLimits.MAX_TARGETS);
        int passed=(int)integer(rawSummary,"passed","report.summary.passed",0,ContractLimits.MAX_TARGETS);
        int failed=(int)integer(rawSummary,"failed","report.summary.failed",0,ContractLimits.MAX_TARGETS);
        int actualPassed=0; boolean degraded=false;
        for(Report.Result result:results){if(result.status()==Report.Status.HEALTHY)actualPassed++; if(result.status()==Report.Status.DEGRADED)degraded=true;}
        if(total!=results.size() || passed+failed!=total || passed!=actualPassed || failed!=results.size()-actualPassed)
            throw error("report.summary","counts contradict results");
        Report.Status expected=failed==0?Report.Status.HEALTHY:(degraded||passed>0?Report.Status.DEGRADED:Report.Status.UNREACHABLE);
        if(status!=expected) throw error("report.status","contradicts result statuses");
        Report.Analysis analysis=null;
        if(source.has("analysis")&&!source.isNull("analysis")) analysis=parseAnalysis(object(source.get("analysis"),"report.analysis"),results.size());
        Report.CompactTopology compact=null;
        if(source.has("compact_topology")) compact=parseCompact(object(source.get("compact_topology"),"report.compact_topology"),results);
        return new Report(id,status,startedAt,duration,new Report.Summary(total,passed,failed),results,analysis,compact);
    }

    private static Report.Result parseResult(JSONObject value,int index) throws JSONException {
        String path="report.results["+index+"]";
        CheckKind kind=kind(text(value,"kind",path+".kind",true,64),path+".kind");
        String address=text(value,"address",path+".address",false,ContractLimits.MAX_STRING_CHARS);
        Report.Status status=enumValue(text(value,"status",path+".status",true,64),Report.Status.class,path+".status");
        long latency=integer(value,"latency_ms",path+".latency_ms",0,Long.MAX_VALUE);
        Instant started=timestamp(value,"started_at",path+".started_at");
        String errorCode=optionalText(value,"error_code",path+".error_code",ContractLimits.MAX_ERROR_CODE_CHARS);
        String message=optionalText(value,"message",path+".message",ContractLimits.MAX_STRING_CHARS);
        Map<String,Object> details=Collections.emptyMap();
        if(value.has("details")&&!value.isNull("details")){
            JSONObject raw=object(value.get("details"),path+".details");
            details=immutableObject(raw,path+".details",0);
            validateKnownDetails(raw,path+".details",kind,status);
        }
        return new Report.Result(kind,address,status,latency,started,errorCode,message,details);
    }

    private static void validateKnownDetails(JSONObject details,String path,CheckKind kind,Report.Status resultStatus) throws JSONException {
        List<String> counterKeys=List.of("attempts_total","attempts_reached","attempts_failed","attempts_unreached","attempts_execution_failed","attempts_timed_out","attempts_cancelled");
        boolean hasCounters=false;for(String key:counterKeys)hasCounters|=details.has(key);
        if((details.has("attempts")||hasCounters)&&kind!=CheckKind.TRACEROUTE)throw error(path,"trace details require traceroute result");
        int total=0,reached=0,unreached=0,executionFailed=0,timedOut=0,cancelled=0;
        boolean hasAttempts=details.has("attempts");
        if(hasAttempts){
            JSONArray attempts=array(details,"attempts",path+".attempts",ContractLimits.MAX_TRACEROUTE_ATTEMPTS);
            if(attempts.length()==0)throw error(path+".attempts","must not be empty");
            Set<Long> seen=new HashSet<>();total=attempts.length();
            for(int i=0;i<attempts.length();i++){
                String attemptPath=path+".attempts["+i+"]";
                JSONObject attempt=object(attempts.get(i),attemptPath);
                long number=integer(attempt,"attempt",attemptPath+".attempt",1,attempts.length());
                if(!seen.add(number)) throw error(path+".attempts","attempt numbers must be unique");
                Report.Status status=enumValue(text(attempt,"status",attemptPath+".status",true,64),Report.Status.class,attemptPath+".status");
                String errorCode=optionalText(attempt,"error_code",attemptPath+".error_code",ContractLimits.MAX_ERROR_CODE_CHARS);
                optionalText(attempt,"message",attemptPath+".message",ContractLimits.MAX_STRING_CHARS);
                JSONObject topology=null;
                if(attempt.has("topology")&&!attempt.isNull("topology")){
                    topology=object(attempt.get("topology"),attemptPath+".topology");
                    validateTopology(topology,attemptPath+".topology",1024,2048);
                }
                if(!errorCode.isEmpty()){
                    if(status!=Report.Status.UNREACHABLE||!Set.of("timeout","cancelled","traceroute_failed").contains(errorCode))throw error(attemptPath,"trace error contradicts status");
                    executionFailed++;if(errorCode.equals("timeout"))timedOut++;if(errorCode.equals("cancelled"))cancelled++;
                }else{
                    if(topology==null||status!=topologyStatus(topology))throw error(attemptPath,"status contradicts topology");
                    if(status==Report.Status.UNREACHABLE)unreached++;else reached++;
                }
            }
            Report.Status expected=reached==0?Report.Status.UNREACHABLE:(reached==total&&allAttemptsHealthy(attempts)?Report.Status.HEALTHY:Report.Status.DEGRADED);
            if(resultStatus!=expected)throw error(path+".attempts","attempts contradict result status");
        }
        if(hasCounters){
            for(String key:counterKeys)if(!details.has(key))throw error(path,"trace counters must be complete");
            int parsedTotal=(int)integer(details,"attempts_total",path+".attempts_total",1,ContractLimits.MAX_TRACEROUTE_ATTEMPTS);
            int parsedReached=(int)integer(details,"attempts_reached",path+".attempts_reached",0,parsedTotal);
            int parsedFailed=(int)integer(details,"attempts_failed",path+".attempts_failed",0,parsedTotal);
            int parsedUnreached=(int)integer(details,"attempts_unreached",path+".attempts_unreached",0,parsedTotal);
            int parsedExecution=(int)integer(details,"attempts_execution_failed",path+".attempts_execution_failed",0,parsedTotal);
            int parsedTimedOut=(int)integer(details,"attempts_timed_out",path+".attempts_timed_out",0,parsedTotal);
            int parsedCancelled=(int)integer(details,"attempts_cancelled",path+".attempts_cancelled",0,parsedTotal);
            if(parsedReached+parsedFailed!=parsedTotal||parsedReached+parsedUnreached+parsedExecution!=parsedTotal||parsedTimedOut+parsedCancelled>parsedExecution)throw error(path,"trace counters contradict each other");
            if(hasAttempts&&(parsedTotal!=total||parsedReached!=reached||parsedFailed!=total-reached||parsedUnreached!=unreached||parsedExecution!=executionFailed||parsedTimedOut!=timedOut||parsedCancelled!=cancelled))throw error(path,"trace counters contradict attempts");
            if((resultStatus==Report.Status.HEALTHY&&parsedReached!=parsedTotal)||(resultStatus==Report.Status.DEGRADED&&parsedReached==0)||(resultStatus==Report.Status.UNREACHABLE&&parsedReached!=0))throw error(path,"trace counters contradict result status");
        }
        if(details.has("topology")&&!details.isNull("topology")) validateTopology(object(details.get("topology"),path+".topology"),path+".topology",1024,2048);
    }

    private static boolean allAttemptsHealthy(JSONArray attempts)throws JSONException{
        for(int i=0;i<attempts.length();i++)if(!"healthy".equals(object(attempts.get(i),"attempt").getString("status")))return false;
        return true;
    }

    private static Report.Status topologyStatus(JSONObject topology)throws JSONException{
        if(!booleanValue(topology,"reached","topology.reached"))return Report.Status.UNREACHABLE;
        JSONArray nodes=array(topology,"nodes","topology.nodes",1024);
        for(int i=0;i<nodes.length();i++){
            String status=text(object(nodes.get(i),"topology.nodes["+i+"]"),"status","topology.nodes["+i+"].status",true,32);
            if(status.equals("unknown")||status.equals("degraded"))return Report.Status.DEGRADED;
        }
        return Report.Status.HEALTHY;
    }

    private static void validateTopology(JSONObject topology,String path,int maxNodes,int maxLinks) throws JSONException {
        booleanValue(topology,"reached",path+".reached");
        JSONArray nodes=array(topology,"nodes",path+".nodes",maxNodes);
        JSONArray links=array(topology,"links",path+".links",maxLinks);
        Set<String> ids=new HashSet<>();
        for(int i=0;i<nodes.length();i++){
            JSONObject node=object(nodes.get(i),path+".nodes["+i+"]");
            String id=text(node,"id",path+".nodes["+i+"].id",true,ContractLimits.MAX_STRING_CHARS);
            if(!ids.add(id))throw error(path+".nodes","node IDs must be unique");
            integer(node,"hop",path+".nodes["+i+"].hop",0,255);
            String status=text(node,"status",path+".nodes["+i+"].status",true,32);
            if(!Set.of("healthy","degraded","unknown","failure").contains(status))throw error(path+".nodes["+i+"].status","unsupported value");
            if(node.has("public_ip"))booleanValue(node,"public_ip",path+".nodes["+i+"].public_ip");
            if(node.has("latency_ms")&&!node.isNull("latency_ms"))finite(node,"latency_ms",path+".nodes["+i+"].latency_ms",0,ContractLimits.MAX_TIMEOUT_MS);
        }
        for(int i=0;i<links.length();i++){
            JSONObject link=object(links.get(i),path+".links["+i+"]");
            String from=text(link,"from",path+".links["+i+"].from",true,ContractLimits.MAX_STRING_CHARS);
            String to=text(link,"to",path+".links["+i+"].to",true,ContractLimits.MAX_STRING_CHARS);
            if(!ids.contains(from)||!ids.contains(to))throw error(path+".links["+i+"]","references unknown node");
            String status=text(link,"status",path+".links["+i+"].status",true,32);
            if(!Set.of("healthy","degraded","unknown","failure").contains(status))throw error(path+".links["+i+"].status","unsupported value");
            if(link.has("latency_ms")&&!link.isNull("latency_ms"))finite(link,"latency_ms",path+".links["+i+"].latency_ms",0,ContractLimits.MAX_TIMEOUT_MS);
        }
    }

    private static Report.Analysis parseAnalysis(JSONObject source,int resultCount) throws JSONException {
        Report.Verdict verdict=enumValue(text(source,"verdict","report.analysis.verdict",true,64),Report.Verdict.class,"report.analysis.verdict");
        JSONArray rf=array(source,"findings","report.analysis.findings",ContractLimits.MAX_FINDINGS);
        JSONArray re=array(source,"evidence","report.analysis.evidence",ContractLimits.MAX_EVIDENCE);
        JSONArray ra=array(source,"actions","report.analysis.actions",ContractLimits.MAX_ACTIONS);
        List<Report.Finding> findings=new ArrayList<>(); List<Report.Evidence> evidence=new ArrayList<>(); List<Report.Action> actions=new ArrayList<>();
        Set<String> findingIds=new HashSet<>(), evidenceIds=new HashSet<>(), actionIds=new HashSet<>();
        for(int i=0;i<rf.length();i++){
            String path="report.analysis.findings["+i+"]"; JSONObject item=object(rf.get(i),path);
            String id=unique(text(item,"id",path+".id",true,ContractLimits.MAX_STRING_CHARS),findingIds,path+".id");
            Report.FindingCode code=enumValue(text(item,"code",path+".code",true,128),Report.FindingCode.class,path+".code");
            Report.Severity severity=enumValue(text(item,"severity",path+".severity",true,64),Report.Severity.class,path+".severity");
            Report.Category category=enumValue(text(item,"category",path+".category",true,64),Report.Category.class,path+".category");
            Report.Confidence confidence=enumValue(text(item,"confidence",path+".confidence",true,64),Report.Confidence.class,path+".confidence");
            findings.add(new Report.Finding(id,code,severity,category,text(item,"title",path+".title",true,4096),text(item,"summary",path+".summary",true,4096),confidence,stringArray(item,"evidence_ids",path+".evidence_ids",ContractLimits.MAX_EVIDENCE),stringArray(item,"action_ids",path+".action_ids",ContractLimits.MAX_ACTIONS)));
        }
        for(int i=0;i<re.length();i++){
            String path="report.analysis.evidence["+i+"]";JSONObject item=object(re.get(i),path);
            String id=unique(text(item,"id",path+".id",true,4096),evidenceIds,path+".id");
            int resultIndex=(int)integer(item,"result_index",path+".result_index",0,Math.max(0,resultCount-1));
            if(resultCount==0)throw error(path+".result_index","references unknown result");
            Integer attempt=item.has("attempt")&&!item.isNull("attempt")?(int)integer(item,"attempt",path+".attempt",1,10):null;
            evidence.add(new Report.Evidence(id,resultIndex,kind(text(item,"kind",path+".kind",true,64),path+".kind"),attempt,text(item,"address",path+".address",false,4096),text(item,"signal",path+".signal",true,4096),text(item,"observed",path+".observed",false,4096),optionalText(item,"expected",path+".expected",4096),enumValue(text(item,"provenance",path+".provenance",true,64),Report.Provenance.class,path+".provenance")));
        }
        for(int i=0;i<ra.length();i++){
            String path="report.analysis.actions["+i+"]";JSONObject item=object(ra.get(i),path);
            String id=unique(text(item,"id",path+".id",true,4096),actionIds,path+".id");
            actions.add(new Report.Action(id,text(item,"title",path+".title",true,4096),text(item,"step",path+".step",true,4096),text(item,"expected_result",path+".expected_result",true,4096),text(item,"escalation_condition",path+".escalation_condition",true,4096)));
        }
        if(verdict==Report.Verdict.ATTENTION&&findings.isEmpty())throw error("report.analysis.findings","attention verdict requires a finding");
        for(Report.Finding finding:findings){for(String id:finding.evidenceIds())if(!evidenceIds.contains(id))throw error("report.analysis.findings","unknown evidence reference");for(String id:finding.actionIds())if(!actionIds.contains(id))throw error("report.analysis.findings","unknown action reference");}
        Report.Coverage coverage=parseCoverage(object(required(source,"coverage","report.analysis.coverage"),"report.analysis.coverage"),resultCount);
        return new Report.Analysis(verdict,findings,evidence,actions,coverage);
    }

    private static Report.Coverage parseCoverage(JSONObject value,int resultCount) throws JSONException {
        List<String> available=stringArray(value,"available","report.analysis.coverage.available",ContractLimits.MAX_COVERAGE_ITEMS);
        List<String> missing=stringArray(value,"missing","report.analysis.coverage.missing",ContractLimits.MAX_COVERAGE_ITEMS);
        return new Report.Coverage(available,missing,coverageIssues(value,"provider_failures",resultCount),coverageIssues(value,"limitations",resultCount));
    }
    private static List<Report.CoverageIssue> coverageIssues(JSONObject source,String key,int resultCount) throws JSONException {
        String base="report.analysis.coverage."+key;JSONArray array=array(source,key,base,ContractLimits.MAX_COVERAGE_ITEMS);List<Report.CoverageIssue> result=new ArrayList<>();
        for(int i=0;i<array.length();i++){
            String path=base+"["+i+"]";JSONObject item=object(array.get(i),path);
            if(resultCount==0)throw error(path+".result_index","references unknown result");
            int resultIndex=(int)integer(item,"result_index",path+".result_index",0,resultCount-1);
            result.add(new Report.CoverageIssue(enumValue(text(item,"code",path+".code",true,64),Report.CoverageCode.class,path+".code"),resultIndex,kind(text(item,"kind",path+".kind",true,64),path+".kind"),optionalText(item,"signal",path+".signal",4096),text(item,"reason",path+".reason",true,4096)));
        }return result;
    }

    private static Report.CompactTopology parseCompact(JSONObject value,List<Report.Result> results) throws JSONException {
        String path="report.compact_topology";
        int resultCount=results.size();
        String schema=text(value,"schema",path+".schema",true,64);if(!schema.equals("compact-v1"))throw error(path+".schema","unsupported value");
        String selection=text(value,"selection",path+".selection",true,64);if(!selection.equals("fair-complete-prefix-v1"))throw error(path+".selection","unsupported value");
        JSONObject limits=object(required(value,"limits",path+".limits"),path+".limits");
        exactInteger(limits,"nodes",500,path+".limits.nodes");exactInteger(limits,"links",1000,path+".limits.links");exactInteger(limits,"max_response_bytes_exclusive",1048576,path+".limits.max_response_bytes_exclusive");exactInteger(limits,"max_geo_bundle_bytes",4096,path+".limits.max_geo_bundle_bytes");
        JSONArray nodes=nullableCollectionArray(value,"nodes",path+".nodes",500),links=nullableCollectionArray(value,"links",path+".links",1000),routes=nullableCollectionArray(value,"routes",path+".routes",ContractLimits.MAX_TARGETS*10);
        Set<String> nodeIds=new HashSet<>();int publicNodes=0,includedGeoBundles=0;
        for(int i=0;i<nodes.length();i++){
            String itemPath=path+".nodes["+i+"]";JSONObject node=object(nodes.get(i),itemPath);
            String id=text(node,"id",itemPath+".id",true,4096);if(!nodeIds.add(id))throw error(path+".nodes","node IDs must be unique");
            enumString(node,"kind",Set.of("local","ip","hostname","unknown"),itemPath+".kind");
            enumString(node,"status",Set.of("healthy","degraded","unknown","failure"),itemPath+".status");
            long hopMin=integer(node,"hop_min",itemPath+".hop_min",0,255),hopMax=integer(node,"hop_max",itemPath+".hop_max",0,255);if(hopMin>hopMax)throw error(itemPath,"hop range is reversed");
            integer(node,"observations",itemPath+".observations",1,ContractLimits.MAX_TOPOLOGY_NODES_TOTAL);
            if(node.has("address")&&!node.isNull("address"))text(node,"address",itemPath+".address",false,4096);
            boolean publicIP=node.has("public_ip")&&booleanValue(node,"public_ip",itemPath+".public_ip");
            if(node.has("latency_ms_avg"))finite(node,"latency_ms_avg",itemPath+".latency_ms_avg",0,Long.MAX_VALUE);
            boolean hasGeo=node.has("geolocation")&&!node.isNull("geolocation");
            boolean hasASN=node.has("asn")&&!node.isNull("asn");
            if((hasGeo||hasASN)&&!publicIP)throw error(itemPath,"Geo bundle requires public_ip");
            if(hasGeo)validateCompactGeoLocation(object(node.get("geolocation"),itemPath+".geolocation"),itemPath+".geolocation");
            if(hasASN)validateCompactAsn(object(node.get("asn"),itemPath+".asn"),itemPath+".asn");
            if(hasGeo||hasASN){
                JSONObject bundle=new JSONObject();if(hasGeo)bundle.put("geolocation",node.get("geolocation"));if(hasASN)bundle.put("asn",node.get("asn"));
                if(bundle.toString().getBytes(StandardCharsets.UTF_8).length-1>ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES)throw error(itemPath,"Geo bundle exceeds byte limit");
            }
            if(publicIP)publicNodes++;
            if(hasGeo||hasASN)includedGeoBundles++;
        }
        Set<String> linkKeys=new HashSet<>();
        for(int i=0;i<links.length();i++){
            String itemPath=path+".links["+i+"]";JSONObject link=object(links.get(i),itemPath);
            String from=text(link,"from",itemPath+".from",true,4096),to=text(link,"to",itemPath+".to",true,4096);
            if(!nodeIds.contains(from)||!nodeIds.contains(to)||from.equals(to))throw error(itemPath,"invalid node reference");
            if(!linkKeys.add(from+"\u0000"+to))throw error(path+".links","directed links must be unique");
            enumString(link,"status",Set.of("healthy","degraded","unknown","failure"),itemPath+".status");
            integer(link,"observations",itemPath+".observations",1,ContractLimits.MAX_TOPOLOGY_LINKS_TOTAL);
        }
        long[][] displayedByResult=new long[resultCount][5];
        long displayedNodeObservations=0,displayedLinkObservations=0;
        for(int i=0;i<routes.length();i++){
            String itemPath=path+".routes["+i+"]";JSONObject route=object(routes.get(i),itemPath);
            if(resultCount==0)throw error(itemPath+".result_index","references unknown result");
            int resultIndex=(int)integer(route,"result_index",itemPath+".result_index",0,resultCount-1);
            if(results.get(resultIndex).kind()!=CheckKind.TRACEROUTE)throw error(itemPath+".result_index","route requires traceroute result");
            integer(route,"attempt",itemPath+".attempt",1,10);
            Report.Status routeStatus=enumValue(text(route,"status",itemPath+".status",true,64),Report.Status.class,itemPath+".status");
            boolean reached=booleanValue(route,"reached",itemPath+".reached");boolean complete=booleanValue(route,"complete",itemPath+".complete");
            if(reached==(routeStatus==Report.Status.UNREACHABLE))throw error(itemPath,"reached contradicts status");
            JSONArray routeNodes=array(route,"node_ids",itemPath+".node_ids",32);if(routeNodes.length()==0)throw error(itemPath+".node_ids","must not be empty");
            String previous=null;
            for(int j=0;j<routeNodes.length();j++){
                Object raw=routeNodes.get(j);if(!(raw instanceof String)||!nodeIds.contains(raw))throw error(itemPath+".node_ids","references unknown node");
                String current=(String)raw;if(current.equals(previous))throw error(itemPath+".node_ids","consecutive node IDs must differ");
                if(previous!=null&&!linkKeys.contains(previous+"\u0000"+current))throw error(itemPath,"route edge has no directed link");
                previous=current;
            }
            displayedByResult[resultIndex][0]++;
            displayedByResult[resultIndex][complete?1:2]++;
            displayedByResult[resultIndex][3]+=routeNodes.length();
            displayedByResult[resultIndex][4]+=Math.max(0,routeNodes.length()-1);
            displayedNodeObservations+=routeNodes.length();displayedLinkObservations+=Math.max(0,routeNodes.length()-1);
        }
        JSONObject stats=object(required(value,"stats",path+".stats"),path+".stats");
        long[] nodeStats=countStats(stats,"nodes",path+".stats.nodes",ContractLimits.MAX_TOPOLOGY_NODES_TOTAL);
        long[] linkStats=countStats(stats,"links",path+".stats.links",ContractLimits.MAX_TOPOLOGY_LINKS_TOTAL);
        long[] routeStats=routeStats(stats,"routes",path+".stats.routes");
        long[] nodeObservationStats=countStats(stats,"node_observations",path+".stats.node_observations",ContractLimits.MAX_TOPOLOGY_NODES_TOTAL);
        long[] linkObservationStats=countStats(stats,"link_observations",path+".stats.link_observations",ContractLimits.MAX_TOPOLOGY_LINKS_TOTAL);
        long linkAggregateObservations=0;for(int i=0;i<links.length();i++)linkAggregateObservations+=integer(object(links.get(i),path+".links["+i+"]"),"observations",path+".links["+i+"].observations",1,ContractLimits.MAX_TOPOLOGY_LINKS_TOTAL);
        if(nodeStats[1]!=nodes.length()||linkStats[1]!=links.length()||routeStats[1]!=routes.length()||
                routeStats[2]!=countRoutes(displayedByResult,1)||routeStats[3]!=countRoutes(displayedByResult,2)||
                nodeObservationStats[1]!=displayedNodeObservations||linkObservationStats[1]!=displayedLinkObservations||
                linkObservationStats[1]!=linkAggregateObservations)throw error(path+".stats","displayed counts contradict arrays");
        JSONObject geo=object(required(value,"geo",path+".geo"),path+".geo");
        long eligible=integer(geo,"eligible",path+".geo.eligible",0,500),available=integer(geo,"available",path+".geo.available",0,500),included=integer(geo,"included",path+".geo.included",0,500),omitted=integer(geo,"omitted",path+".geo.omitted",0,500),unavailable=integer(geo,"unavailable",path+".geo.unavailable",0,500);
        if(eligible!=available+unavailable||available!=included+omitted||eligible!=publicNodes||included!=includedGeoBundles)throw error(path+".geo","counts contradict nodes");
        boolean truncated=booleanValue(value,"truncated",path+".truncated");
        JSONArray reasons=optionalArray(value,"truncation_reasons",path+".truncation_reasons",4);Set<String> seen=new HashSet<>();
        List<String> allowed=List.of("node_limit","link_limit","response_size","geo_metadata_limit");int last=-1;
        for(int i=0;i<reasons.length();i++){Object raw=reasons.get(i);if(!(raw instanceof String))throw error(path+".truncation_reasons","expected strings");int order=allowed.indexOf(raw);if(order<0||order<=last||!seen.add((String)raw))throw error(path+".truncation_reasons","unsupported order");last=order;}
        if(truncated!=(reasons.length()>0))throw error(path+".truncated","contradicts reasons");
        JSONArray resultStats=array(value,"result_stats",path+".result_stats",ContractLimits.MAX_TARGETS);
        if(resultStats.length()!=resultCount)throw error(path+".result_stats","must contain one entry per result");
        long[] sums=new long[11];
        for(int i=0;i<resultStats.length();i++){
            String p=path+".result_stats["+i+"]";JSONObject item=object(resultStats.get(i),p);
            if(integer(item,"result_index",p+".result_index",0,resultCount-1)!=i)throw error(path+".result_stats","entries must be ordered by result");
            long[] rs=routeStats(item,"routes",p+".routes");long[] ns=countStats(item,"node_observations",p+".node_observations",ContractLimits.MAX_TOPOLOGY_NODES_TOTAL);long[] ls=countStats(item,"link_observations",p+".link_observations",ContractLimits.MAX_TOPOLOGY_LINKS_TOTAL);
            if(results.get(i).kind()!=CheckKind.TRACEROUTE&&(rs[0]!=0||ns[0]!=0||ls[0]!=0))throw error(p,"non-traceroute result cannot have route stats");
            if(rs[1]!=displayedByResult[i][0]||rs[2]!=displayedByResult[i][1]||rs[3]!=displayedByResult[i][2]||ns[1]!=displayedByResult[i][3]||ls[1]!=displayedByResult[i][4])throw error(p,"displayed counts contradict routes");
            for(int j=0;j<5;j++)sums[j]+=rs[j];for(int j=0;j<3;j++){sums[5+j]+=ns[j];sums[8+j]+=ls[j];}
        }
        for(int i=0;i<5;i++)if(sums[i]!=routeStats[i])throw error(path+".result_stats","route sums contradict global stats");
        for(int i=0;i<3;i++)if(sums[5+i]!=nodeObservationStats[i]||sums[8+i]!=linkObservationStats[i])throw error(path+".result_stats","observation sums contradict global stats");
        return new Report.CompactTopology(schema,selection,nodes.length(),links.length(),routes.length(),truncated,immutableObject(value,path,0));
    }

    private static long countRoutes(long[][] counts,int column){long total=0;for(long[] count:counts)total+=count[column];return total;}
    private static void validateCompactGeoLocation(JSONObject geo,String path)throws JSONException{
        if(geo.has("city"))text(geo,"city",path+".city",false,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES);
        if(geo.has("region"))text(geo,"region",path+".region",false,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES);
        if(geo.has("country"))text(geo,"country",path+".country",false,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES);
        if(geo.has("country_code"))text(geo,"country_code",path+".country_code",false,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES);
        finite(geo,"latitude",path+".latitude",-90,90);finite(geo,"longitude",path+".longitude",-180,180);
    }
    private static void validateCompactAsn(JSONObject asn,String path)throws JSONException{
        integer(asn,"number",path+".number",0,4_294_967_295L);
        if(asn.has("organization"))text(asn,"organization",path+".organization",false,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES);
    }

    private static long[] countStats(JSONObject parent,String key,String path,long max) throws JSONException {
        JSONObject value=object(required(parent,key,path),path);long total=integer(value,"total",path+".total",0,max),displayed=integer(value,"displayed",path+".displayed",0,max),omitted=integer(value,"omitted",path+".omitted",0,max);if(total!=displayed+omitted)throw error(path,"counts contradict each other");return new long[]{total,displayed,omitted};
    }
    private static long[] routeStats(JSONObject parent,String key,String path) throws JSONException {
        JSONObject value=object(required(parent,key,path),path);long total=integer(value,"total",path+".total",0,200),displayed=integer(value,"displayed",path+".displayed",0,200),complete=integer(value,"complete",path+".complete",0,200),partial=integer(value,"partial",path+".partial",0,200),omitted=integer(value,"omitted",path+".omitted",0,200);if(total!=displayed+omitted||displayed!=complete+partial)throw error(path,"counts contradict each other");return new long[]{total,displayed,complete,partial,omitted};
    }
    private static String enumString(JSONObject parent,String key,Set<String> allowed,String path) throws JSONException {String value=text(parent,key,path,true,64);if(!allowed.contains(value))throw error(path,"unsupported value");return value;}

    private static void exactInteger(JSONObject o,String key,long expected,String path) throws JSONException {if(integer(o,key,path,expected,expected)!=expected)throw error(path,"unsupported value");}
    private static CheckKind kind(String value,String path){try{return CheckKind.fromWire(value);}catch(IllegalArgumentException e){throw error(path,"unsupported value");}}
    private static <E extends Enum<E>> E enumValue(String value,Class<E> type,String path){for(E candidate:type.getEnumConstants())if(candidate.name().toLowerCase(Locale.ROOT).equals(value))return candidate;throw error(path,"unsupported value");}
    private static String unique(String id,Set<String> ids,String path){if(!ids.add(id))throw error(path,"IDs must be unique");return id;}

    private static Object required(JSONObject o,String key,String path) throws JSONException {if(!o.has(key)||o.isNull(key))throw error(path,"required");return o.get(key);}
    private static JSONObject object(Object value,String path){if(!(value instanceof JSONObject))throw error(path,"expected object");return (JSONObject)value;}
    private static JSONArray array(JSONObject o,String key,String path,int max) throws JSONException {Object value=required(o,key,path);if(!(value instanceof JSONArray))throw error(path,"expected array");JSONArray result=(JSONArray)value;if(result.length()>max)throw error(path,"exceeds limit "+max);return result;}
    private static JSONArray nullableCollectionArray(JSONObject o,String key,String path,int max) throws JSONException {if(!o.has(key))throw error(path,"required");if(o.isNull(key))o.put(key,new JSONArray());return array(o,key,path,max);}
    private static JSONArray optionalArray(JSONObject o,String key,String path,int max) throws JSONException {if(!o.has(key))return new JSONArray();return array(o,key,path,max);}
    private static String text(JSONObject o,String key,String path,boolean nonempty,int max) throws JSONException {Object value=required(o,key,path);if(!(value instanceof String))throw error(path,"expected string");String result=(String)value;if(result.length()>max||(nonempty&&result.isEmpty()))throw error(path,"invalid string length");return result;}
    private static String optionalText(JSONObject o,String key,String path,int max) throws JSONException {if(!o.has(key)||o.isNull(key))return "";Object value=o.get(key);if(!(value instanceof String))throw error(path,"expected string");String result=(String)value;if(result.length()>max)throw error(path,"exceeds string limit");return result;}
    private static long integer(JSONObject o,String key,String path,long min,long max) throws JSONException {Object value=required(o,key,path);if(!(value instanceof Number))throw error(path,"expected integer");String raw=value.toString();if(!raw.matches("-?\\d+"))throw error(path,"expected integer");try{long parsed=new BigDecimal(raw).longValueExact();if(parsed<min||parsed>max)throw error(path,"out of range");return parsed;}catch(ArithmeticException|NumberFormatException e){throw error(path,"expected bounded integer");}}
    private static double finite(JSONObject o,String key,String path,double min,double max) throws JSONException {Object value=required(o,key,path);if(!(value instanceof Number))throw error(path,"expected number");double parsed=((Number)value).doubleValue();if(!Double.isFinite(parsed)||parsed<min||parsed>max)throw error(path,"expected bounded finite number");return parsed;}
    private static boolean booleanValue(JSONObject o,String key,String path) throws JSONException {Object value=required(o,key,path);if(!(value instanceof Boolean))throw error(path,"expected boolean");return (Boolean)value;}
    private static Instant timestamp(JSONObject o,String key,String path) throws JSONException {String value=text(o,key,path,true,4096);try{return Instant.parse(value);}catch(DateTimeParseException e){throw error(path,"expected timestamp");}}
    private static List<String> stringArray(JSONObject o,String key,String path,int max) throws JSONException {JSONArray raw=array(o,key,path,max);List<String> result=new ArrayList<>();for(int i=0;i<raw.length();i++){Object item=raw.get(i);if(!(item instanceof String)||((String)item).length()>4096||((String)item).isEmpty())throw error(path+"["+i+"]","expected non-empty bounded string");result.add((String)item);}return result;}

    private static Map<String,Object> immutableObject(JSONObject source,String path,int depth) throws JSONException {
        if(depth>ContractLimits.MAX_DETAIL_DEPTH)throw error(path,"nested too deeply");if(source.length()>ContractLimits.MAX_DETAIL_KEYS)throw error(path,"too many keys");
        List<String> keys=new ArrayList<>();Iterator<String> iterator=source.keys();while(iterator.hasNext())keys.add(iterator.next());Collections.sort(keys);
        Map<String,Object> result=new LinkedHashMap<>();for(String key:keys){if(key.length()>128)throw error(path,"key too long");result.put(key,immutableValue(source.get(key),path+"."+key,depth+1));}return Collections.unmodifiableMap(result);
    }
    private static Object immutableValue(Object value,String path,int depth) throws JSONException {
        if(value==JSONObject.NULL)return null;if(value instanceof String){if(((String)value).length()>4096)throw error(path,"string too long");return value;}if(value instanceof Boolean||value instanceof Number)return value;
        if(value instanceof JSONObject)return immutableObject((JSONObject)value,path,depth);
        if(value instanceof JSONArray){JSONArray raw=(JSONArray)value;if(raw.length()>ContractLimits.MAX_DETAIL_ARRAY_ITEMS)throw error(path,"array too long");if(depth>ContractLimits.MAX_DETAIL_DEPTH)throw error(path,"nested too deeply");List<Object> result=new ArrayList<>();for(int i=0;i<raw.length();i++)result.add(immutableValue(raw.get(i),path+"["+i+"]",depth+1));return Collections.unmodifiableList(result);}throw error(path,"unsupported JSON value");
    }

    private static void enforceAggregateBudget(Object root) throws JSONException {Budget b=new Budget();walk(root,"",b);}
    private static void walk(Object value,String key,Budget b) throws JSONException {
        if(value instanceof String){b.strings+=((String)value).length();if(b.strings>ContractLimits.MAX_REPORT_STRING_CHARS)throw error("report","aggregate string budget exceeded");return;}
        if(value instanceof JSONObject){if(++b.containers>ContractLimits.MAX_REPORT_CONTAINERS)throw error("report","aggregate container budget exceeded");JSONObject o=(JSONObject)value;Iterator<String> it=o.keys();while(it.hasNext()){String k=it.next();walk(o.get(k),k,b);}return;}
        if(value instanceof JSONArray){if(++b.containers>ContractLimits.MAX_REPORT_CONTAINERS)throw error("report","aggregate container budget exceeded");JSONArray a=(JSONArray)value;if(key.equals("nodes")){b.nodes+=a.length();if(b.nodes>ContractLimits.MAX_TOPOLOGY_NODES_TOTAL)throw error("report","aggregate topology node budget exceeded");}if(key.equals("links")){b.links+=a.length();if(b.links>ContractLimits.MAX_TOPOLOGY_LINKS_TOTAL)throw error("report","aggregate topology link budget exceeded");}for(int i=0;i<a.length();i++)walk(a.get(i),"",b);}
    }
    private static final class Budget{long strings;int containers,nodes,links;}
    private static ReportParseException error(String path,String message){return new ReportParseException("invalid response schema at "+path+": "+message);}
}
