package com.checknetwork.app.core;

import android.util.JsonReader;
import android.util.JsonToken;
import java.io.IOException;
import java.io.StringReader;
import java.math.BigDecimal;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.time.format.DateTimeParseException;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Deque;
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
    private static final Set<String> RESULT_ERROR_CODES = Set.of(
            "cancelled", "checker_capacity_unavailable", "checker_panic", "connection_failed",
            "destination_unreached", "invalid_address", "invalid_url", "network_policy_blocked",
            "response_read_failed", "service_greeting_unverified", "timeout",
            "tls_certificate_expired", "tls_certificate_not_yet_valid", "tls_downgrade",
            "tls_handshake_failed", "tls_hostname_mismatch", "tls_untrusted",
            "traceroute_execution_incomplete", "traceroute_failed", "traceroute_unavailable", "unexpected_status");
    private static final Set<String> HTTP_RESULT_ERRORS = Set.of(
            "network_policy_blocked", "invalid_url", "timeout", "cancelled", "connection_failed",
            "response_read_failed", "checker_panic", "checker_capacity_unavailable", "unexpected_status");
    private static final Set<String> SERVICE_RESULT_ERRORS = Set.of(
            "network_policy_blocked", "invalid_address", "timeout", "cancelled", "connection_failed",
            "checker_panic", "checker_capacity_unavailable");

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
        try (JsonReader reader = new JsonReader(new StringReader(json))) {
            reader.setLenient(false);
            new StrictPrevalidator(reader).validate();
        } catch (ReportParseException exception) {
            throw exception;
        } catch (IOException | RuntimeException exception) {
            throw new ReportParseException("invalid report JSON", exception);
        }
    }

    /**
     * Validates the hostile wire tree before JSONObject can collapse duplicate keys.
     * The explicit frame stack keeps malformed deep input off the Java call stack.
     */
    private static final class StrictPrevalidator {
        private final JsonReader reader;
        private final Deque<JsonFrame> frames = new ArrayDeque<>();
        private long stringChars;
        private int containers;
        private int nodes;
        private int links;

        StrictPrevalidator(JsonReader reader) { this.reader = reader; }

        void validate() throws IOException {
            if (reader.peek() != JsonToken.BEGIN_OBJECT) {
                throw error("report", "expected one JSON object");
            }
            beginContainer(true, "");
            while (!frames.isEmpty()) {
                JsonFrame frame = frames.peek();
                if (!reader.hasNext()) {
                    if (frame.object) reader.endObject();
                    else reader.endArray();
                    frames.pop();
                    continue;
                }
                String owner = "";
                if (frame.object) {
                    owner = reader.nextName();
                    if (!wellFormedUtf16(owner)) throw error("report", "malformed UTF-16 field name");
                    if (!frame.names.add(owner)) throw error("report", "duplicate field");
                } else {
                    if ("nodes".equals(frame.owner) && ++nodes > ContractLimits.MAX_TOPOLOGY_NODES_TOTAL) {
                        throw error("report", "aggregate topology node budget exceeded");
                    }
                    if ("links".equals(frame.owner) && ++links > ContractLimits.MAX_TOPOLOGY_LINKS_TOTAL) {
                        throw error("report", "aggregate topology link budget exceeded");
                    }
                }
                consumeValue(owner);
            }
            if (reader.peek() != JsonToken.END_DOCUMENT) {
                throw error("report", "must contain one JSON object");
            }
        }

        private void consumeValue(String owner) throws IOException {
            JsonToken token = reader.peek();
            switch (token) {
                case BEGIN_OBJECT -> beginContainer(true, owner);
                case BEGIN_ARRAY -> beginContainer(false, owner);
                case STRING -> {
                    String value = reader.nextString();
                    if (!wellFormedUtf16(value)) throw error("report", "malformed UTF-16 string");
                    stringChars += value.length();
                    if (stringChars > ContractLimits.MAX_REPORT_STRING_CHARS) {
                        throw error("report", "aggregate string budget exceeded");
                    }
                }
                case NUMBER -> reader.nextString();
                case BOOLEAN -> reader.nextBoolean();
                case NULL -> reader.nextNull();
                default -> throw error("report", "invalid JSON value");
            }
        }

        private void beginContainer(boolean object, String owner) throws IOException {
            if (++containers > ContractLimits.MAX_REPORT_CONTAINERS) {
                throw error("report", "aggregate container budget exceeded");
            }
            if (frames.size() + 1 > ContractLimits.MAX_JSON_DEPTH) {
                throw error("report", "nested too deeply");
            }
            if (object) reader.beginObject();
            else reader.beginArray();
            frames.push(new JsonFrame(object, owner));
        }
    }

    private static final class JsonFrame {
        final boolean object;
        final String owner;
        final Set<String> names;

        JsonFrame(boolean object, String owner) {
            this.object = object;
            this.owner = owner;
            this.names = object ? new HashSet<>() : Collections.emptySet();
        }
    }

    private static boolean wellFormedUtf16(String value) {
        for (int index = 0; index < value.length(); index++) {
            char current = value.charAt(index);
            if (Character.isHighSurrogate(current)) {
                if (++index >= value.length() || !Character.isLowSurrogate(value.charAt(index))) return false;
            } else if (Character.isLowSurrogate(current)) return false;
        }
        return true;
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
        if(source.has("analysis")&&!source.isNull("analysis")) analysis=parseAnalysis(object(source.get("analysis"),"report.analysis"),results);
        Report.CompactTopology compact=null;
        if(source.has("compact_topology")) compact=parseCompact(object(source.get("compact_topology"),"report.compact_topology"),results);
        return new Report(id,status,startedAt,duration,new Report.Summary(total,passed,failed),results,analysis,compact);
    }

    private static Report.Result parseResult(JSONObject value,int index) throws JSONException {
        String path="report.results["+index+"]";
        allowedFields(value,Set.of("kind","address","status","latency_ms","started_at",
                "error_code","message","details"),path);
        if((value.has("error_code")&&value.isNull("error_code"))
                ||(value.has("message")&&value.isNull("message")))
            throw error(path,"present optional text must not be null");
        CheckKind kind=kind(text(value,"kind",path+".kind",true,64),path+".kind");
        String address=text(value,"address",path+".address",false,ContractLimits.MAX_STRING_CHARS);
        Report.Status status=enumValue(text(value,"status",path+".status",true,64),Report.Status.class,path+".status");
        long latency=integer(value,"latency_ms",path+".latency_ms",0,Long.MAX_VALUE);
        Instant started=timestamp(value,"started_at",path+".started_at");
        String errorCode=optionalText(value,"error_code",path+".error_code",ContractLimits.MAX_ERROR_CODE_CHARS);
        if(!errorCode.isEmpty()&&!RESULT_ERROR_CODES.contains(errorCode))throw error(path+".error_code","unsupported value");
        String message=optionalText(value,"message",path+".message",ContractLimits.MAX_STRING_CHARS);
        Map<String,Object> details=Collections.emptyMap();
        JSONObject rawDetails=null;
        if(value.has("details")&&value.isNull("details"))throw error(path+".details","must not be null");
        if(value.has("details")){
            rawDetails=object(value.get("details"),path+".details");
            details=immutableObject(rawDetails,path+".details",0);
            validateKnownDetails(rawDetails,path+".details",kind,status);
        }
        validateClosedResultShape(value,path,kind,status,errorCode,rawDetails);
        return new Report.Result(kind,address,status,latency,started,errorCode,message,details);
    }

    private static void validateClosedResultShape(JSONObject result,String path,CheckKind kind,Report.Status status,
                                                  String errorCode,JSONObject details) throws JSONException {
        boolean hasErrorCode=result.has("error_code");
        boolean hasDetails=result.has("details");
        Set<String> generalNoDetail=Set.of("cancelled","checker_capacity_unavailable","checker_panic",
                "connection_failed","network_policy_blocked","timeout");
        if(status==Report.Status.HEALTHY&&!errorCode.isEmpty())throw error(path+".error_code","healthy result requires an empty code");
        if(status!=Report.Status.HEALTHY&&kind!=CheckKind.TRACEROUTE&&errorCode.isEmpty())
            throw error(path+".error_code","non-healthy result requires an error code");
        if(generalNoDetail.contains(errorCode)){
            if(status!=Report.Status.UNREACHABLE||!hasErrorCode)throw error(path+".status","contradicts terminal error");
            if(kind==CheckKind.TRACEROUTE&&hasDetails&&(errorCode.equals("cancelled")||errorCode.equals("timeout"))){
                validateTraceResultShape(path,status,errorCode,details,!errorCode.equals("cancelled"));
            }else if(hasDetails&&kind!=CheckKind.DNS&&kind!=CheckKind.TCP)
                throw error(path+".details","must be absent");
            return;
        }
        if(errorCode.equals("invalid_url")||errorCode.equals("response_read_failed")){
            requireShape(result,path,kind,status,Report.Status.UNREACHABLE,Set.of(CheckKind.HTTP,CheckKind.HTTPS),details,Set.of());
            return;
        }
        if(errorCode.equals("invalid_address")){
            requireShape(result,path,kind,status,Report.Status.UNREACHABLE,
                    Set.of(CheckKind.IMAPS,CheckKind.POP3S,CheckKind.SMTPS,CheckKind.TRACEROUTE),details,Set.of());
            return;
        }
        if(errorCode.equals("traceroute_unavailable")){
            requireShape(result,path,kind,status,Report.Status.UNREACHABLE,Set.of(CheckKind.TRACEROUTE),details,Set.of());
            return;
        }
        if(errorCode.equals("service_greeting_unverified")){
            if(!hasErrorCode)throw error(path+".error_code","required");
            requireShape(result,path,kind,status,Report.Status.DEGRADED,serviceKinds(),details,Set.of());
            return;
        }
        if(Set.of("tls_certificate_expired","tls_certificate_not_yet_valid").contains(errorCode)){
            requireShape(result,path,kind,status,Report.Status.UNREACHABLE,tlsKinds(),details,
                    Set.of("certificate_not_before","certificate_not_after"));
            Instant before=canonicalUtcTimestamp(details,"certificate_not_before",path+".details.certificate_not_before");
            Instant after=canonicalUtcTimestamp(details,"certificate_not_after",path+".details.certificate_not_after");
            if(!before.isBefore(after))throw error(path+".details","certificate validity range is invalid");
            return;
        }
        if(Set.of("tls_hostname_mismatch","tls_untrusted","tls_handshake_failed").contains(errorCode)){
            requireShape(result,path,kind,status,Report.Status.UNREACHABLE,tlsKinds(),details,Set.of());
            return;
        }
        if(errorCode.equals("tls_downgrade")){
            requireShape(result,path,kind,status,Report.Status.UNREACHABLE,Set.of(CheckKind.HTTPS),details,Set.of());
            return;
        }
        if(kind==CheckKind.DNS&&status==Report.Status.HEALTHY&&errorCode.isEmpty()){
            if(hasErrorCode)throw error(path+".error_code","must be absent");
            requireDetails(result,details,path,Set.of("addresses","answer_count"));
            stringArray(details,"addresses",path+".details.addresses",ContractLimits.MAX_DETAIL_ARRAY_ITEMS);
            integer(details,"answer_count",path+".details.answer_count",0,ContractLimits.MAX_DETAIL_ARRAY_ITEMS);
            return;
        }
        if(kind==CheckKind.TCP&&status==Report.Status.HEALTHY&&errorCode.isEmpty()){
            if(hasErrorCode)throw error(path+".error_code","must be absent");
            requireDetails(result,details,path,Set.of("local_address","remote_address"));
            text(details,"local_address",path+".details.local_address",false,ContractLimits.MAX_STRING_CHARS);
            text(details,"remote_address",path+".details.remote_address",false,ContractLimits.MAX_STRING_CHARS);
            return;
        }
        if((kind==CheckKind.HTTP||kind==CheckKind.HTTPS)
                &&((status==Report.Status.HEALTHY&&errorCode.isEmpty())
                ||(status==Report.Status.UNREACHABLE&&hasErrorCode&&errorCode.equals("unexpected_status")))){
            if(status==Report.Status.HEALTHY&&hasErrorCode)throw error(path+".error_code","must be absent");
            if(details!=null)validateHTTPDetails(details,path+".details");
            return;
        }
        if(isServiceKind(kind)&&status==Report.Status.HEALTHY&&errorCode.isEmpty()){
            if(hasErrorCode)throw error(path+".error_code","must be absent");
            if(details==null)throw error(path+".details","required");
            Set<String> allowed=isImplicitTlsServiceKind(kind)
                    ?Set.of("certificate_expires_at","certificate_subject","cipher_suite","tls_version","verification_scope")
                    :Set.of("verification_scope");
            if(!allowed.containsAll(jsonKeys(details))||!details.has("verification_scope")
                    ||(isImplicitTlsServiceKind(kind)&&!jsonKeys(details).containsAll(allowed)))
                throw error(path+".details","does not match a producer service detail shape");
            if(!"server_greeting".equals(text(details,"verification_scope",path+".details.verification_scope",true,64)))
                throw error(path+".details.verification_scope","unsupported value");
            if(isImplicitTlsServiceKind(kind))validateOptionalTLSObservationDetails(details,path+".details");
            return;
        }
        if(kind==CheckKind.TRACEROUTE){
            boolean validOutcome=(status==Report.Status.HEALTHY&&errorCode.isEmpty())
                    ||(status==Report.Status.DEGRADED&&errorCode.isEmpty())
                    ||(status==Report.Status.UNREACHABLE&&Set.of("destination_unreached","traceroute_failed",
                    "traceroute_execution_incomplete").contains(errorCode));
            if(!validOutcome)throw error(path,"unsupported traceroute result status/error shape");
            if((status==Report.Status.HEALTHY||status==Report.Status.DEGRADED)&&hasErrorCode)
                throw error(path+".error_code","must be absent");
            if(details==null)throw error(path+".details","required");
            validateTraceResultShape(path,status,errorCode,details,true);
            return;
        }
        throw error(path,"unsupported producer result shape");
    }

    private static void validateHTTPDetails(JSONObject details,String path)throws JSONException{
        Set<String> allowed=Set.of("certificate_expires_at","certificate_subject","cipher_suite","content_type",
                "expected_status","protocol","status_code","tls_version");
        if(!allowed.containsAll(jsonKeys(details)))throw error(path,"contains unsupported HTTP detail");
        if(details.has("content_type"))text(details,"content_type",path+".content_type",false,ContractLimits.MAX_STRING_CHARS);
        if(details.has("expected_status"))integer(details,"expected_status",path+".expected_status",0,Integer.MAX_VALUE);
        if(details.has("protocol"))text(details,"protocol",path+".protocol",false,ContractLimits.MAX_STRING_CHARS);
        if(details.has("status_code"))integer(details,"status_code",path+".status_code",0,Integer.MAX_VALUE);
        if(details.has("tls_version")){
            String value=text(details,"tls_version",path+".tls_version",true,ContractLimits.MAX_STRING_CHARS);
            if(value.trim().isEmpty())throw error(path+".tls_version","must not be blank");
        }
        if(details.has("cipher_suite"))text(details,"cipher_suite",path+".cipher_suite",false,ContractLimits.MAX_STRING_CHARS);
        if(details.has("certificate_subject"))text(details,"certificate_subject",path+".certificate_subject",false,ContractLimits.MAX_STRING_CHARS);
        if(details.has("certificate_expires_at"))utcTimestamp(details,"certificate_expires_at",path+".certificate_expires_at");
    }

    private static void validateOptionalTLSObservationDetails(JSONObject details,String path)throws JSONException{
        if(details.has("tls_version")){
            String tls=text(details,"tls_version",path+".tls_version",true,ContractLimits.MAX_STRING_CHARS);
            if(tls.trim().isEmpty())throw error(path+".tls_version","must not be blank");
        }
        if(details.has("cipher_suite"))text(details,"cipher_suite",path+".cipher_suite",false,ContractLimits.MAX_STRING_CHARS);
        if(details.has("certificate_subject"))text(details,"certificate_subject",path+".certificate_subject",false,ContractLimits.MAX_STRING_CHARS);
        if(details.has("certificate_expires_at"))utcTimestamp(details,"certificate_expires_at",path+".certificate_expires_at");
    }

    private static void requireDetails(JSONObject result,JSONObject details,String path,Set<String> keys)throws JSONException{
        if(!result.has("details")||details==null)throw error(path+".details","required");
        exactFields(details,keys,path+".details");
    }

    private static void validateTLSObservationDetails(JSONObject details,String path,boolean includesScope)throws JSONException{
        String tls=text(details,"tls_version",path+".tls_version",true,ContractLimits.MAX_STRING_CHARS);
        if(tls.trim().isEmpty())throw error(path+".tls_version","must not be blank");
        text(details,"cipher_suite",path+".cipher_suite",false,ContractLimits.MAX_STRING_CHARS);
        text(details,"certificate_subject",path+".certificate_subject",false,ContractLimits.MAX_STRING_CHARS);
        utcTimestamp(details,"certificate_expires_at",path+".certificate_expires_at");
        if(includesScope&&!details.has("verification_scope"))throw error(path+".verification_scope","required");
    }

    private static void validateTraceResultShape(String path,Report.Status status,String errorCode,
                                                 JSONObject details,boolean requireCounters)throws JSONException{
        Set<String> allowed=new HashSet<>(Set.of("attempts","attempts_cancelled","attempts_execution_failed",
                "attempts_failed","attempts_reached","attempts_timed_out","attempts_total","attempts_unreached",
                "geoip_enrichment","geoip_provider_failures"));
        boolean topologyAllowed=false;
        if(status==Report.Status.HEALTHY||status==Report.Status.DEGRADED){
            if(!errorCode.isEmpty())throw error(path+".error_code","must be empty");
            topologyAllowed=true;
        }else if(status==Report.Status.UNREACHABLE&&errorCode.equals("destination_unreached")){
            topologyAllowed=true;
        }else if(status==Report.Status.UNREACHABLE
                &&Set.of("cancelled","timeout","traceroute_failed").contains(errorCode)){
            topologyAllowed=true;
        }else if(status==Report.Status.UNREACHABLE&&errorCode.equals("traceroute_execution_incomplete")){
            topologyAllowed=true;
        }else throw error(path,"unsupported traceroute result status/error shape");
        boolean hasTopology=details.has("topology"),hasEnrichment=details.has("geoip_enrichment"),
                hasFailures=details.has("geoip_provider_failures");
        if(hasTopology&&details.isNull("topology"))throw error(path+".details.topology","must not be null");
        if(hasEnrichment&&details.isNull("geoip_enrichment"))throw error(path+".details.geoip_enrichment","must not be null");
        if(!topologyAllowed&&hasTopology)
            throw error(path+".details.topology","contradicts traceroute outcome");
        if(hasTopology)allowed.add("topology");
        if(!allowed.containsAll(jsonKeys(details)))throw error(path+".details","contains unsupported traceroute detail");
        boolean hasAttempts=details.has("attempts");
        boolean hasCounters=details.has("attempts_total")||details.has("attempts_reached")
                ||details.has("attempts_failed")||details.has("attempts_unreached")
                ||details.has("attempts_execution_failed")||details.has("attempts_timed_out")
                ||details.has("attempts_cancelled");
        if(requireCounters){
            for(String key:Set.of("attempts_total","attempts_reached","attempts_failed","attempts_unreached",
                    "attempts_execution_failed","attempts_timed_out","attempts_cancelled"))
                if(!details.has(key))throw error(path+".details."+key,"required");
        }
        if(!hasAttempts&&!hasCounters&&!jsonKeys(details).isEmpty())
            throw error(path+".details","missing traceroute attempt information");
        if(hasFailures)integer(details,"geoip_provider_failures",path+".details.geoip_provider_failures",0,Integer.MAX_VALUE);
        if(hasEnrichment)validateResultEnrichment(object(details.get("geoip_enrichment"),path+".details.geoip_enrichment"),path+".details.geoip_enrichment");
    }

    private static void validateResultEnrichment(JSONObject value,String path)throws JSONException{
        exactFields(value,Set.of("provider","source","cache_hits","upstream_fetches","max_age_ms","failures"),path);
        if(!"geoip".equals(text(value,"provider",path+".provider",true,64)))throw error(path+".provider","unsupported value");
        int cache=(int)integer(value,"cache_hits",path+".cache_hits",0,6200);
        int upstream=(int)integer(value,"upstream_fetches",path+".upstream_fetches",0,6200);
        String source=text(value,"source",path+".source",true,64);
        String expected=cache>0?(upstream>0?"mixed":"cache"):(upstream>0?"upstream":"none");
        if(!source.equals(expected))throw error(path+".source","contradicts success counts");
        integer(value,"max_age_ms",path+".max_age_ms",0,86_400_000);
        JSONArray failures=array(value,"failures",path+".failures",8);
        for(int i=0;i<failures.length();i++){
            JSONObject failure=object(failures.get(i),path+".failures["+i+"]");
            exactFields(failure,Set.of("kind","count","retryable"),path+".failures["+i+"]");
            String kind=text(failure,"kind",path+".failures["+i+"].kind",true,64);
            if(!Set.of("busy","cancelled","malformed","not_found","policy","rate_limited","timeout","unavailable").contains(kind))
                throw error(path+".failures["+i+"].kind","unsupported value");
            integer(failure,"count",path+".failures["+i+"].count",1,6200);
            booleanValue(failure,"retryable",path+".failures["+i+"].retryable");
        }
    }

    private static void requireShape(JSONObject result,String path,CheckKind kind,Report.Status actual,Report.Status expected,
                                     Set<CheckKind> kinds,JSONObject details,Set<String> detailKeys) throws JSONException {
        if(actual!=expected)throw error(path+".status","contradicts error code");
        if(!kinds.contains(kind))throw error(path+".kind","contradicts error code");
        if(detailKeys.isEmpty()){
            if(result.has("details"))throw error(path+".details","must be absent");
        }else{
            if(details==null)throw error(path+".details","required");
            exactFields(details,detailKeys,path+".details");
        }
    }

    private static Set<String> jsonKeys(JSONObject value){
        Set<String> keys=new HashSet<>();Iterator<String> iterator=value.keys();while(iterator.hasNext())keys.add(iterator.next());return keys;
    }
    private static Set<CheckKind> serviceKinds(){return Set.of(CheckKind.IMAP,CheckKind.IMAPS,CheckKind.POP3,CheckKind.POP3S,CheckKind.SMTP,CheckKind.SMTPS,CheckKind.SSH,CheckKind.SUBMISSION);}
    private static Set<CheckKind> serviceAndTraceKinds(){return Set.of(CheckKind.IMAP,CheckKind.IMAPS,CheckKind.POP3,CheckKind.POP3S,CheckKind.SMTP,CheckKind.SMTPS,CheckKind.SSH,CheckKind.SUBMISSION,CheckKind.TRACEROUTE);}
    private static Set<CheckKind> tlsKinds(){return Set.of(CheckKind.HTTPS,CheckKind.IMAPS,CheckKind.POP3S,CheckKind.SMTPS);}
    private static boolean isServiceKind(CheckKind kind){return serviceKinds().contains(kind);}
    private static boolean isImplicitTlsServiceKind(CheckKind kind){return kind==CheckKind.IMAPS||kind==CheckKind.POP3S||kind==CheckKind.SMTPS;}
    private static Instant canonicalUtcTimestamp(JSONObject value,String key,String path)throws JSONException{
        String text=text(value,key,path,true,64);
        if(!text.matches("\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}Z"))throw error(path,"expected canonical UTC RFC3339 timestamp");
        try{Instant parsed=Instant.parse(text);if(!parsed.toString().equals(text))throw error(path,"expected canonical UTC RFC3339 timestamp");return parsed;}
        catch(DateTimeParseException exception){throw error(path,"expected canonical UTC RFC3339 timestamp");}
    }
    private static Instant utcTimestamp(JSONObject value,String key,String path)throws JSONException{
        String text=text(value,key,path,true,4096);
        if(!text.endsWith("Z"))throw error(path,"expected UTC timestamp");
        try{return Instant.parse(text);}catch(DateTimeParseException exception){throw error(path,"expected UTC timestamp");}
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
            total=attempts.length();
            for(int i=0;i<attempts.length();i++){
                String attemptPath=path+".attempts["+i+"]";
                JSONObject attempt=object(attempts.get(i),attemptPath);
                integer(attempt,"attempt",attemptPath+".attempt",1,ContractLimits.MAX_TRACEROUTE_ATTEMPTS);
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
            if(link.has("latency_ms"))throw error(path+".links["+i+"].latency_ms","unsupported link field; expected latency_delta_ms");
            if(link.has("latency_delta_ms"))finite(link,"latency_delta_ms",path+".links["+i+"].latency_delta_ms",0,ContractLimits.MAX_TIMEOUT_MS);
        }
    }

    private static Report.Analysis parseAnalysis(JSONObject source,List<Report.Result> results) throws JSONException {
        int resultCount=results.size();
        Report.Verdict verdict=enumValue(text(source,"verdict","report.analysis.verdict",true,64),Report.Verdict.class,"report.analysis.verdict");
        JSONArray rf=array(source,"findings","report.analysis.findings",ContractLimits.MAX_FINDINGS);
        JSONArray re=array(source,"evidence","report.analysis.evidence",ContractLimits.MAX_EVIDENCE);
        JSONArray ra=array(source,"actions","report.analysis.actions",ContractLimits.MAX_ACTIONS);
        List<Report.Finding> findings=new ArrayList<>(); List<Report.Evidence> evidence=new ArrayList<>(); List<Report.Action> actions=new ArrayList<>();
        Set<String> findingIds=new HashSet<>(), evidenceIds=new HashSet<>(), actionIds=new HashSet<>();
        for(int i=0;i<rf.length();i++){
            String path="report.analysis.findings["+i+"]"; JSONObject item=object(rf.get(i),path);
            String id=unique(text(item,"id",path+".id",true,ContractLimits.MAX_STRING_CHARS),findingIds,path+".id");
            Report.FindingCode code=findingCode(text(item,"code",path+".code",true,128),path+".code");
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
            CheckKind evidenceKind=kind(text(item,"kind",path+".kind",true,64),path+".kind");
            if(evidenceKind!=results.get(resultIndex).kind())throw error(path+".kind","contradicts referenced result");
            Integer attempt=item.has("attempt")&&!item.isNull("attempt")?(int)integer(item,"attempt",path+".attempt",1,10):null;
            if(attempt!=null)validateEvidenceAttempt(results.get(resultIndex),attempt,path+".attempt");
            evidence.add(new Report.Evidence(id,resultIndex,evidenceKind,attempt,text(item,"address",path+".address",false,4096),text(item,"signal",path+".signal",true,4096),text(item,"observed",path+".observed",false,4096),optionalText(item,"expected",path+".expected",4096),enumValue(text(item,"provenance",path+".provenance",true,64),Report.Provenance.class,path+".provenance")));
        }
        for(int i=0;i<ra.length();i++){
            String path="report.analysis.actions["+i+"]";JSONObject item=object(ra.get(i),path);
            String id=unique(text(item,"id",path+".id",true,4096),actionIds,path+".id");
            actions.add(new Report.Action(id,text(item,"title",path+".title",true,4096),text(item,"step",path+".step",true,4096),text(item,"expected_result",path+".expected_result",true,4096),text(item,"escalation_condition",path+".escalation_condition",true,4096)));
        }
        if(verdict==Report.Verdict.ATTENTION&&findings.isEmpty())throw error("report.analysis.findings","attention verdict requires a finding");
        for(Report.Finding finding:findings){for(String id:finding.evidenceIds())if(!evidenceIds.contains(id))throw error("report.analysis.findings","unknown evidence reference");for(String id:finding.actionIds())if(!actionIds.contains(id))throw error("report.analysis.findings","unknown action reference");}
        Report.Coverage coverage=parseCoverage(object(required(source,"coverage","report.analysis.coverage"),"report.analysis.coverage"),results);
        return new Report.Analysis(verdict,findings,evidence,actions,coverage);
    }

    private static void validateEvidenceAttempt(Report.Result result,int attempt,String path){
        if(result.kind()!=CheckKind.TRACEROUTE)throw error(path,"requires traceroute result");
        Object rawAttempts=result.details().get("attempts");
        if(rawAttempts instanceof List<?> attempts){
            boolean found=false;
            for(Object raw:attempts){
                if(raw instanceof Map<?,?> item&&item.get("attempt") instanceof Number number&&number.intValue()==attempt){found=true;break;}
            }
            if(!found)throw error(path,"references unavailable attempt");
            return;
        }
        Object rawTotal=result.details().get("attempts_total");
        if(!(rawTotal instanceof Number total)||attempt>total.intValue())throw error(path,"references unavailable attempt");
    }

    private static Report.Coverage parseCoverage(JSONObject value,List<Report.Result> results) throws JSONException {
        List<String> available=stringArray(value,"available","report.analysis.coverage.available",ContractLimits.MAX_COVERAGE_ITEMS);
        List<String> missing=stringArray(value,"missing","report.analysis.coverage.missing",ContractLimits.MAX_COVERAGE_ITEMS);
        List<Report.EnrichmentCoverage> enrichment=value.has("enrichment")
                ? enrichment(value,"enrichment","report.analysis.coverage.enrichment") : Collections.emptyList();
        return new Report.Coverage(available,missing,coverageIssues(value,"provider_failures",results),coverageIssues(value,"limitations",results),enrichment);
    }

    private static List<Report.EnrichmentCoverage> enrichment(JSONObject source,String key,String path) throws JSONException {
        JSONArray entries=array(source,key,path,1);
        List<Report.EnrichmentCoverage> result=new ArrayList<>();
        for(int i=0;i<entries.length();i++){
            String itemPath=path+"["+i+"]";
            JSONObject item=object(entries.get(i),itemPath);
            exactFields(item,Set.of("provider","source","cache_hits","upstream_fetches","max_age_ms","failures"),itemPath);
            String provider=text(item,"provider",itemPath+".provider",true,64);
            if(!provider.equals("geoip"))throw error(itemPath+".provider","unsupported value");
            int cacheHits=(int)integer(item,"cache_hits",itemPath+".cache_hits",0,6200);
            int upstreamFetches=(int)integer(item,"upstream_fetches",itemPath+".upstream_fetches",0,6200);
            long total=(long)cacheHits+upstreamFetches;
            if(total>6200)throw error(itemPath,"lookup count exceeds limit 6200");
            JSONArray rawFailures=array(item,"failures",itemPath+".failures",8);
            List<Report.EnrichmentFailure> failures=new ArrayList<>();
            String previous="";
            for(int j=0;j<rawFailures.length();j++){
                String failurePath=itemPath+".failures["+j+"]";
                JSONObject failure=object(rawFailures.get(j),failurePath);
                exactFields(failure,Set.of("kind","count","retryable"),failurePath);
                String kindText=text(failure,"kind",failurePath+".kind",true,64);
                Report.EnrichmentFailureKind kind=enumValue(kindText,Report.EnrichmentFailureKind.class,failurePath+".kind");
                if(kindText.compareTo(previous)<=0)throw error(itemPath+".failures","must contain unique kinds in canonical order");
                previous=kindText;
                int count=(int)integer(failure,"count",failurePath+".count",1,6200);
                boolean retryable=booleanValue(failure,"retryable",failurePath+".retryable");
                if(retryable!=enrichmentRetryable(kind))throw error(failurePath+".retryable","contradicts failure kind");
                total+=count;
                if(total>6200)throw error(itemPath,"lookup count exceeds limit 6200");
                failures.add(new Report.EnrichmentFailure(kind,count,retryable));
            }
            Report.EnrichmentSource expected=cacheHits>0
                    ? (upstreamFetches>0?Report.EnrichmentSource.MIXED:Report.EnrichmentSource.CACHE)
                    : (upstreamFetches>0?Report.EnrichmentSource.UPSTREAM:Report.EnrichmentSource.NONE);
            Report.EnrichmentSource parsedSource=enumValue(text(item,"source",itemPath+".source",true,64),Report.EnrichmentSource.class,itemPath+".source");
            if(parsedSource!=expected)throw error(itemPath+".source","contradicts success counts");
            long maxAgeMs=integer(item,"max_age_ms",itemPath+".max_age_ms",0,86_400_000);
            result.add(new Report.EnrichmentCoverage(provider,parsedSource,cacheHits,upstreamFetches,maxAgeMs,failures));
        }
        return result;
    }

    private static boolean enrichmentRetryable(Report.EnrichmentFailureKind kind){
        return switch(kind){
            case BUSY, RATE_LIMITED, TIMEOUT, UNAVAILABLE -> true;
            case CANCELLED, MALFORMED, NOT_FOUND, POLICY -> false;
        };
    }
    private static List<Report.CoverageIssue> coverageIssues(JSONObject source,String key,List<Report.Result> results) throws JSONException {
        int resultCount=results.size();
        String base="report.analysis.coverage."+key;JSONArray array=array(source,key,base,ContractLimits.MAX_COVERAGE_ITEMS);List<Report.CoverageIssue> result=new ArrayList<>();
        for(int i=0;i<array.length();i++){
            String path=base+"["+i+"]";JSONObject item=object(array.get(i),path);
            if(resultCount==0)throw error(path+".result_index","references unknown result");
            int resultIndex=(int)integer(item,"result_index",path+".result_index",0,resultCount-1);
            CheckKind issueKind=kind(text(item,"kind",path+".kind",true,64),path+".kind");
            if(issueKind!=results.get(resultIndex).kind())throw error(path+".kind","contradicts referenced result");
            result.add(new Report.CoverageIssue(enumValue(text(item,"code",path+".code",true,64),Report.CoverageCode.class,path+".code"),resultIndex,issueKind,optionalText(item,"signal",path+".signal",4096),text(item,"reason",path+".reason",true,4096)));
        }return result;
    }

    private static Report.CompactTopology parseCompact(JSONObject value,List<Report.Result> results) throws JSONException {
        String path="report.compact_topology";
        allowedFields(value,Set.of("schema","selection","limits","nodes","links","routes","stats","result_stats","geo","truncated","truncation_reasons"),path);
        int resultCount=results.size();
        String schema=text(value,"schema",path+".schema",true,64);if(!schema.equals("compact-v1"))throw error(path+".schema","unsupported value");
        String selection=text(value,"selection",path+".selection",true,64);if(!selection.equals("fair-complete-prefix-v1"))throw error(path+".selection","unsupported value");
        JSONObject limits=object(required(value,"limits",path+".limits"),path+".limits");
        exactFields(limits,Set.of("nodes","links","max_response_bytes_exclusive","max_geo_bundle_bytes"),path+".limits");
        exactInteger(limits,"nodes",500,path+".limits.nodes");exactInteger(limits,"links",1000,path+".limits.links");exactInteger(limits,"max_response_bytes_exclusive",1048576,path+".limits.max_response_bytes_exclusive");exactInteger(limits,"max_geo_bundle_bytes",4096,path+".limits.max_geo_bundle_bytes");
        JSONArray nodes=array(value,"nodes",path+".nodes",500),links=array(value,"links",path+".links",1000),routes=array(value,"routes",path+".routes",ContractLimits.MAX_TARGETS*10);
        Set<String> nodeIds=new HashSet<>();int publicNodes=0,includedGeoBundles=0;
        for(int i=0;i<nodes.length();i++){
            String itemPath=path+".nodes["+i+"]";JSONObject node=object(nodes.get(i),itemPath);
            allowedFields(node,Set.of("id","kind","address","status","hop_min","hop_max","latency_ms_avg","observations","public_ip","geolocation","asn"),itemPath);
            String id=text(node,"id",itemPath+".id",true,4096);if(!nodeIds.add(id))throw error(path+".nodes","node IDs must be unique");
            enumString(node,"kind",Set.of("local","ip","hostname","unknown"),itemPath+".kind");
            enumString(node,"status",Set.of("healthy","degraded","unknown","failure"),itemPath+".status");
            long hopMin=integer(node,"hop_min",itemPath+".hop_min",0,255),hopMax=integer(node,"hop_max",itemPath+".hop_max",0,255);if(hopMin>hopMax)throw error(itemPath,"hop range is reversed");
            integer(node,"observations",itemPath+".observations",1,ContractLimits.MAX_TOPOLOGY_NODES_TOTAL);
            if(node.has("address"))text(node,"address",itemPath+".address",true,4096);
            boolean publicIP=node.has("public_ip")&&booleanValue(node,"public_ip",itemPath+".public_ip");
            if(node.has("public_ip")&&!publicIP)throw error(itemPath+".public_ip","present value must be true");
            if(node.has("latency_ms_avg"))finite(node,"latency_ms_avg",itemPath+".latency_ms_avg",0,Long.MAX_VALUE);
            boolean hasGeo=node.has("geolocation");
            boolean hasASN=node.has("asn");
            if((hasGeo||hasASN)&&!publicIP)throw error(itemPath,"Geo bundle requires public_ip");
            if(hasGeo)validateCompactGeoLocation(object(node.get("geolocation"),itemPath+".geolocation"),itemPath+".geolocation");
            if(hasASN)validateCompactAsn(object(node.get("asn"),itemPath+".asn"),itemPath+".asn");
            if(hasGeo||hasASN){
                if(compactGeoBundleContribution(hasGeo,hasGeo?object(node.get("geolocation"),itemPath+".geolocation"):null,
                        hasASN,hasASN?object(node.get("asn"),itemPath+".asn"):null,itemPath)
                        >ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES)throw error(itemPath,"Geo bundle exceeds byte limit");
            }
            if(publicIP)publicNodes++;
            if(hasGeo||hasASN)includedGeoBundles++;
        }
        Set<String> linkKeys=new HashSet<>();
        for(int i=0;i<links.length();i++){
            String itemPath=path+".links["+i+"]";JSONObject link=object(links.get(i),itemPath);
            exactFields(link,Set.of("from","to","status","observations"),itemPath);
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
            exactFields(route,Set.of("result_index","attempt","status","reached","complete","node_ids"),itemPath);
            if(resultCount==0)throw error(itemPath+".result_index","references unknown result");
            int resultIndex=(int)integer(route,"result_index",itemPath+".result_index",0,resultCount-1);
            if(results.get(resultIndex).kind()!=CheckKind.TRACEROUTE)throw error(itemPath+".result_index","route requires traceroute result");
            int attempt=(int)integer(route,"attempt",itemPath+".attempt",1,10);
            validateEvidenceAttempt(results.get(resultIndex),attempt,itemPath+".attempt");
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
        exactFields(stats,Set.of("nodes","links","routes","node_observations","link_observations"),path+".stats");
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
        exactFields(geo,Set.of("eligible","available","included","omitted","unavailable"),path+".geo");
        long eligible=integer(geo,"eligible",path+".geo.eligible",0,500),available=integer(geo,"available",path+".geo.available",0,500),included=integer(geo,"included",path+".geo.included",0,500),omitted=integer(geo,"omitted",path+".geo.omitted",0,500),unavailable=integer(geo,"unavailable",path+".geo.unavailable",0,500);
        if(eligible!=available+unavailable||available!=included+omitted||eligible!=publicNodes||included!=includedGeoBundles)throw error(path+".geo","counts contradict nodes");
        boolean truncated=booleanValue(value,"truncated",path+".truncated");
        JSONArray reasons=optionalArray(value,"truncation_reasons",path+".truncation_reasons",4);Set<String> seen=new HashSet<>();
        if(value.has("truncation_reasons")&&reasons.length()==0)throw error(path+".truncation_reasons","present array must not be empty");
        List<String> allowed=List.of("node_limit","link_limit","response_size","geo_metadata_limit");int last=-1;
        for(int i=0;i<reasons.length();i++){Object raw=reasons.get(i);if(!(raw instanceof String))throw error(path+".truncation_reasons","expected strings");int order=allowed.indexOf(raw);if(order<0||order<=last||!seen.add((String)raw))throw error(path+".truncation_reasons","unsupported order");last=order;}
        if(truncated!=(reasons.length()>0))throw error(path+".truncated","contradicts reasons");
        JSONArray resultStats=array(value,"result_stats",path+".result_stats",ContractLimits.MAX_TARGETS);
        if(resultStats.length()!=resultCount)throw error(path+".result_stats","must contain one entry per result");
        long[] sums=new long[11];
        for(int i=0;i<resultStats.length();i++){
            String p=path+".result_stats["+i+"]";JSONObject item=object(resultStats.get(i),p);
            exactFields(item,Set.of("result_index","routes","node_observations","link_observations"),p);
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
        allowedFields(geo,Set.of("city","region","country","country_code","latitude","longitude"),path);
        if(geo.has("city"))text(geo,"city",path+".city",true,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES);
        if(geo.has("region"))text(geo,"region",path+".region",true,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES);
        if(geo.has("country"))text(geo,"country",path+".country",true,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES);
        if(geo.has("country_code"))text(geo,"country_code",path+".country_code",true,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES);
        finite(geo,"latitude",path+".latitude",-90,90);finite(geo,"longitude",path+".longitude",-180,180);
    }
    private static void validateCompactAsn(JSONObject asn,String path)throws JSONException{
        allowedFields(asn,Set.of("number","organization"),path);
        long number=asn.has("number")?integer(asn,"number",path+".number",1,4_294_967_295L):0;
        String organization=asn.has("organization")?text(asn,"organization",path+".organization",true,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES):"";
        if(number==0&&organization.isEmpty())throw error(path,"empty ASN is not canonical");
    }

    private static int compactGeoBundleContribution(boolean hasGeo,JSONObject geo,boolean hasASN,JSONObject asn,String path)throws JSONException{
        int bytes=0;
        if(hasGeo){
            bytes+=",\"geolocation\":{".length();boolean first=true;
            for(String key:List.of("city","region","country","country_code")){
                String value=geo.has(key)?text(geo,key,path+".geolocation."+key,false,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES):"";
                if(value.isEmpty())continue;
                bytes+=(first?0:1)+key.length()+3+goJSONStringBytes(value,path+".geolocation."+key);first=false;
            }
            bytes+=(first?0:1)+"latitude".length()+3+goJSONNumberBytes(finite(geo,"latitude",path+".geolocation.latitude",-90,90));
            bytes+=1+"longitude".length()+3+goJSONNumberBytes(finite(geo,"longitude",path+".geolocation.longitude",-180,180))+1;
        }
        if(hasASN){
            bytes+=",\"asn\":{".length();boolean first=true;
            long number=asn.has("number")?integer(asn,"number",path+".asn.number",0,4_294_967_295L):0;
            String organization=asn.has("organization")?text(asn,"organization",path+".asn.organization",false,ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES):"";
            if(number!=0){bytes+="number".length()+3+Long.toString(number).length();first=false;}
            if(!organization.isEmpty())bytes+=(first?0:1)+"organization".length()+3+goJSONStringBytes(organization,path+".asn.organization");
            bytes++;
        }
        return bytes;
    }

    private static int goJSONStringBytes(String value,String path){
        int encoded=2,utf8=0;
        for(int i=0;i<value.length();i++){
            char code=value.charAt(i);
            if(Character.isHighSurrogate(code)){
                if(i+1>=value.length()||!Character.isLowSurrogate(value.charAt(i+1)))throw error(path,"malformed UTF-16 surrogate");
                encoded+=4;utf8+=4;i++;
            }else if(Character.isLowSurrogate(code)){
                throw error(path,"malformed UTF-16 surrogate");
            }else{
                utf8+=code<=0x7f?1:code<=0x7ff?2:3;
                if(code=='\"'||code=='\\'||code=='\b'||code=='\t'||code=='\n'||code=='\f'||code=='\r')encoded+=2;
                else if(code<0x20||code=='<'||code=='>'||code=='&'||code=='\u2028'||code=='\u2029')encoded+=6;
                else encoded+=code<=0x7f?1:code<=0x7ff?2:3;
            }
            if(utf8>ContractLimits.MAX_COMPACT_GEO_BUNDLE_BYTES)throw error(path,"Geo string exceeds byte limit");
        }
        return encoded;
    }

    private static int goJSONNumberBytes(double value){
        if(value==0&&Double.doubleToRawLongBits(value)==Double.doubleToRawLongBits(-0.0d))return 2;
        double absolute=Math.abs(value);
        BigDecimal decimal=BigDecimal.valueOf(value).stripTrailingZeros();
        String text=absolute!=0&&(absolute<1e-6||absolute>=1e21)?decimal.toString().toLowerCase(Locale.ROOT):decimal.toPlainString();
        return text.replace("e+","e").length();
    }

    private static long[] countStats(JSONObject parent,String key,String path,long max) throws JSONException {
        JSONObject value=object(required(parent,key,path),path);exactFields(value,Set.of("total","displayed","omitted"),path);long total=integer(value,"total",path+".total",0,max),displayed=integer(value,"displayed",path+".displayed",0,max),omitted=integer(value,"omitted",path+".omitted",0,max);if(total!=displayed+omitted)throw error(path,"counts contradict each other");return new long[]{total,displayed,omitted};
    }
    private static long[] routeStats(JSONObject parent,String key,String path) throws JSONException {
        JSONObject value=object(required(parent,key,path),path);exactFields(value,Set.of("total","displayed","complete","partial","omitted"),path);long total=integer(value,"total",path+".total",0,200),displayed=integer(value,"displayed",path+".displayed",0,200),complete=integer(value,"complete",path+".complete",0,200),partial=integer(value,"partial",path+".partial",0,200),omitted=integer(value,"omitted",path+".omitted",0,200);if(total!=displayed+omitted||displayed!=complete+partial)throw error(path,"counts contradict each other");return new long[]{total,displayed,complete,partial,omitted};
    }
    private static String enumString(JSONObject parent,String key,Set<String> allowed,String path) throws JSONException {String value=text(parent,key,path,true,64);if(!allowed.contains(value))throw error(path,"unsupported value");return value;}

    private static void exactInteger(JSONObject o,String key,long expected,String path) throws JSONException {if(integer(o,key,path,expected,expected)!=expected)throw error(path,"unsupported value");}
    private static CheckKind kind(String value,String path){try{return CheckKind.fromWire(value);}catch(IllegalArgumentException e){throw error(path,"unsupported value");}}
    private static Report.FindingCode findingCode(String value,String path){try{return Report.FindingCode.fromWire(value);}catch(IllegalArgumentException e){throw error(path,"unsupported value");}}
    private static <E extends Enum<E>> E enumValue(String value,Class<E> type,String path){for(E candidate:type.getEnumConstants())if(candidate.name().toLowerCase(Locale.ROOT).equals(value))return candidate;throw error(path,"unsupported value");}
    private static String unique(String id,Set<String> ids,String path){if(!ids.add(id))throw error(path,"IDs must be unique");return id;}
    private static void exactFields(JSONObject value,Set<String> expected,String path){
        if(value.length()!=expected.size())throw error(path,"expected exact fields");
        Iterator<String> keys=value.keys();while(keys.hasNext())if(!expected.contains(keys.next()))throw error(path,"expected exact fields");
    }
    private static void allowedFields(JSONObject value,Set<String> allowed,String path){
        Iterator<String> keys=value.keys();while(keys.hasNext())if(!allowed.contains(keys.next()))throw error(path,"contains unsupported field");
    }

    private static Object required(JSONObject o,String key,String path) throws JSONException {if(!o.has(key)||o.isNull(key))throw error(path,"required");return o.get(key);}
    private static JSONObject object(Object value,String path){if(!(value instanceof JSONObject))throw error(path,"expected object");return (JSONObject)value;}
    private static JSONArray array(JSONObject o,String key,String path,int max) throws JSONException {Object value=required(o,key,path);if(!(value instanceof JSONArray))throw error(path,"expected array");JSONArray result=(JSONArray)value;if(result.length()>max)throw error(path,"exceeds limit "+max);return result;}

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

    private static ReportParseException error(String path,String message){return new ReportParseException("invalid response schema at "+path+": "+message);}
}
