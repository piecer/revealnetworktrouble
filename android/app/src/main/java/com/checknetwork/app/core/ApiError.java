package com.checknetwork.app.core;

import java.math.BigInteger;
import java.time.DateTimeException;
import java.time.Instant;
import java.time.ZonedDateTime;
import java.time.format.DateTimeFormatter;
import java.time.format.DateTimeParseException;
import java.util.Objects;
import java.util.Optional;
import java.util.Set;
import org.json.JSONException;
import org.json.JSONObject;

/** Allowlisted operational API error; server-provided prose is never retained. */
public final class ApiError {
    private final int status;
    private final String code;
    private final String safeMessage;
    private final boolean retryable;
    private final Instant retryAt;

    private ApiError(int status,String code,String safeMessage,boolean retryable,Instant retryAt){
        this.status=status;this.code=code;this.safeMessage=safeMessage;this.retryable=retryable;this.retryAt=retryAt;
    }

    public static ApiError parse(int status,String body,String retryAfter,Instant now){
        Objects.requireNonNull(now,"now");
        String defaultCode;String message;boolean retryable;
        Set<String> allowed;
        switch(status){
            case 401: defaultCode="unauthorized";message="Authorization credential was rejected (HTTP 401).";retryable=false;allowed=Set.of("unauthorized");break;
            case 422: defaultCode="unprocessable_entity";message="The request could not be processed (HTTP 422).";retryable=false;allowed=Set.of("invalid_request","network_policy_blocked");break;
            case 429: defaultCode="rate_limited";message="The request was rate limited (HTTP 429).";retryable=true;allowed=Set.of("rate_limited");break;
            case 500: defaultCode="internal_error";message="The server encountered an internal error (HTTP 500).";retryable=true;allowed=Set.of("internal_error","compact_response_too_large","response_serialization_failed");break;
            case 503: defaultCode="server_busy";message="The server is busy (HTTP 503).";retryable=true;allowed=Set.of("server_busy");break;
            default:
                defaultCode=status==0?"http_error":"http_"+status;
                if(defaultCode.length()>32)defaultCode="http_error";
                message="The server returned an error (HTTP "+status+").";
                retryable=status>=500;
                allowed=Set.of();
                break;
        }
        String code=structuredCode(body,allowed,defaultCode);
        Instant retryAt=retryable?parseRetryAfter(retryAfter,now):null;
        return new ApiError(status,code,message,retryable,retryAt);
    }

    private static String structuredCode(String body,Set<String> allowed,String fallback){
        if(body==null||body.trim().isEmpty())return fallback;
        try{
            JSONObject root=new JSONObject(body);Object rawError=root.opt("error");
            if(!(rawError instanceof JSONObject))return fallback;
            Object rawCode=((JSONObject)rawError).opt("code");
            return rawCode instanceof String&&allowed.contains(rawCode)?(String)rawCode:fallback;
        }catch(JSONException|RuntimeException ignored){return fallback;}
    }

    public static Instant parseRetryAfter(String value,Instant now){
        if(value==null)return null;String text=value.trim();if(text.isEmpty())return null;
        if(text.matches("\\d+")){
            try{BigInteger seconds=new BigInteger(text);if(seconds.bitLength()>62)return null;return now.plusSeconds(seconds.longValue());}
            catch(ArithmeticException|DateTimeException ignored){return null;}
        }
        try{Instant parsed=ZonedDateTime.parse(text,DateTimeFormatter.RFC_1123_DATE_TIME).toInstant();return parsed.isBefore(now)?null:parsed;}
        catch(DateTimeParseException ignored){return null;}
    }

    public int status(){return status;} public String code(){return code;} public String safeMessage(){return safeMessage;}
    public boolean retryable(){return retryable;} public Optional<Instant> retryAt(){return Optional.ofNullable(retryAt);}
    @Override public String toString(){return "ApiError{status="+status+", code='"+code+"', retryable="+retryable+"}";}
}
