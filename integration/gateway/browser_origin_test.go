package gateway_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// Opt-in native-browser probe: open the printed owned URL in a real browser.
// No user accounts/state or production bearer are exposed to this fixture.
func TestBrowserFetchOriginAndEvents(t *testing.T) {
	if os.Getenv("AGW_BROWSER_PROBE") != "1" {
		t.Skip("requires interactive real browser")
	}
	result := make(chan string, 1)
	var page atomic.Value
	page.Store([]byte{})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page.Load().([]byte))
	})
	mux.HandleFunc("/events-fetch.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("../..", "examples/gateway/events-fetch.js"))
	})
	mux.HandleFunc("/result", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
		if err != nil {
			http.Error(w, "bad report", 400)
			return
		}
		select {
		case result <- string(data):
		default:
		}
		w.WriteHeader(204)
	})
	browser := httptest.NewServer(mux)
	defer browser.Close()
	f := gatewayHTTPOriginFixture(t, false, []string{browser.URL}, true)
	cfg, _ := json.Marshal(map[string]string{"base": f.server.URL, "token": f.token})
	page.Store([]byte(`<!doctype html><meta charset="utf-8"><title>AIRC browser fixture</title><h1>AIRC browser fixture</h1><pre id="status">Running…</pre><script type="module">
import { roomEvents, messageText } from "/events-fetch.js";
const config=` + string(cfg) + `;
const status=document.querySelector("#status");
const check=(ok,message)=>{if(!ok)throw new Error(message);status.textContent+="\nPASS "+message;};
const call=(path,options={})=>fetch(config.base+path,{credentials:"omit",redirect:"error",headers:{Authorization:"Bearer "+config.token,...options.headers},...options});
try {
 let r=await call("/v1/capabilities");check(r.status===200,"cross-origin bearer capability discovery");
 r=await fetch(config.base+"/v1/capabilities",{credentials:"omit"});check(r.status===401,"origin without bearer has no authority");
 r=await call("/v1/directory");const directory=await r.json();check(r.status===200 && directory.agents.some(card=>card.nick==="fixture-author"),"public directory read through browser preflight");
 r=await call("/v1/directory?nick=FiXtUrE-AuThOr&limit=1");const namedDirectory=await r.json();check(r.status===200 && namedDirectory.agents.length===1 && namedDirectory.agents[0].nick==="fixture-author","native nickname directory query");
 const body={version:1,request_id:"browser-send",operation:"send",target:{kind:"room",name:"#gateway-fixture"},message:"browser whole\nbody 🙂"};
 r=await call("/v1/messages",{method:"POST",headers:{Authorization:"Bearer "+config.token,"Content-Type":"application/json","Idempotency-Key":"browser-send"},body:JSON.stringify(body)});
 check(r.status===202 && r.headers.get("Location")==="/v1/requests/browser-send","preflight JSON mutation and exposed Location");
 let accepted=false;
 for(let i=0;i<30;i++){r=await call("/v1/requests/browser-send");const value=await r.json();if(value.state==="accepted"){accepted=true;break;}await new Promise(resolve=>setTimeout(resolve,100));}
 check(accepted,"scoped receipt status");
 r=await call("/v1/requests/browser-send",{method:"PUT",headers:{Authorization:"Bearer "+config.token,"Content-Type":"application/json","Idempotency-Key":"browser-send"},body:JSON.stringify(body)});const repeated=await r.json();check(r.status===202 && r.headers.get("Location")==="/v1/requests/browser-send" && repeated.state==="accepted" && repeated.send_attempts===1,"PUT preflight and cross-method coalescing");
 let cursor="",count=0;
 for await(const frame of roomEvents(config.base,config.token,{target:"#gateway-fixture",start:"retained"})){
  if(frame.event==="page"){check(frame.value.messages.length===1 && messageText(frame.value.messages[0])===body.message,"SSE complete whole-body page");cursor=frame.id;count++;break;}
 }
 check(count===1 && cursor.startsWith("gc1."),"opaque SSE checkpoint and caller cancellation");
 const cancel=new AbortController();
 for await(const frame of roomEvents(config.base,config.token,{target:"#gateway-fixture",cursor,signal:cancel.signal})){
  if(frame.event==="page"){check(frame.value.messages.length===0 && frame.id!=="","Last-Event-ID preflight resumes without repeating message");break;}
 }
 status.textContent+="\nALL PASS";
 await fetch("/result",{method:"POST",body:"PASS"});
}catch(error){status.textContent+="\nFAIL "+error.message;await fetch("/result",{method:"POST",body:"FAIL "+error.message});}
</script>`))
	fmt.Println("BROWSER_FIXTURE_URL=" + browser.URL)
	select {
	case report := <-result:
		if report != "PASS" {
			t.Fatal(report)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("browser probe timed out")
	}
	observed := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.Message == "browser whole\nbody 🙂" })
	if observed.From != "fixture-author" {
		t.Fatal("browser identity changed", observed.From)
	}
}
