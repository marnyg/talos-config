# Libation: (re)creating the Audible credential

`sealed-secret.yaml` holds Libation's `AccountsSettings.json` (the
Audible device registration) and the `libation-master.key` that
encrypts the tokens inside it. Both are minted once by a browser
sign-in; the nightly CronJob then refreshes its access token from them
and never needs a person. Redo this when the registration dies: Amazon
password changed, "sign out of all devices", the device deregistered
under *Manage Your Content and Devices*, or Libation switching device
profile. Symptom: the Job fails at `scan`.

The password never leaves the browser. What crosses the terminal is a
single-use, PKCE-bound authorization code; what gets sealed is the
registration the CLI exchanged it for.

## 1. Login pod

A throwaway pod from the CronJob's image, `sleep` instead of the
entrypoint, an emptyDir at `/config`, uid 1000 like the CronJob:

```sh
kubectl apply -n media -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata: {name: libation-login}
spec:
  restartPolicy: Never
  securityContext: {runAsNonRoot: true, runAsUser: 1000, runAsGroup: 1000, fsGroup: 1000, seccompProfile: {type: RuntimeDefault}}
  containers:
    - name: libation
      image: docker.io/rmcrackan/libation:14.2.2   # match cronjob.yaml
      command: [sleep, "7200"]
      env: [{name: HOME, value: /tmp}]
      securityContext: {allowPrivilegeEscalation: false, capabilities: {drop: [ALL]}}
      volumeMounts: [{name: config, mountPath: /config}, {name: tmp, mountPath: /tmp}]
  volumes: [{name: config, emptyDir: {}}, {name: tmp, emptyDir: {}}]
EOF
kubectl wait -n media pod/libation-login --for=condition=Ready
# The CLI refuses to start without a Settings.json.
kubectl exec -n media libation-login -- sh -c 'echo "{\"Books\": \"/tmp\"}" > /config/Settings.json'
```

## 2. Sign in

Do **not** paste the URL into an interactive `kubectl exec -it`: the
terminal wraps the paste in bracketed-paste escapes that the pod's tty
passes through, and the CLI rejects the result as "not a valid
absolute URL" (2026-10-20). Run the CLI under a pty fed from a FIFO
instead, and deliver the URL as a file.

```sh
E=you@example.com
kubectl exec -n media libation-login -- sh -c "rm -f /tmp/in /tmp/out; mkfifo /tmp/in; (sleep 3600 > /tmp/in &); (script -q -f -c '/libation/LibationCli login-external --libationFiles /config --locale au --account $E' /dev/null < /tmp/in > /tmp/out 2>&1 &); sleep 8; cat /tmp/out"
```

Open the printed Amazon URL in a browser, sign in. The final page
(`…/ap/maplanding?…authorization_code=…`) may look like an error;
copy its whole address-bar URL, then:

```sh
pbpaste > /tmp/resp.txt
kubectl cp /tmp/resp.txt media/libation-login:/tmp/resp.txt
kubectl exec -n media libation-login -- sh -c 'cat /tmp/resp.txt > /tmp/in; sleep 5; tail -n 3 /tmp/out'
rm /tmp/resp.txt
```

Expect `Successfully authenticated account '…' (australia).` The PKCE
verifier lives in that one CLI process: if anything fails, restart from
the `mkfifo` line (new URL), not from the paste.

The CLI also writes `/config/libation-master.key` ("LAST-RESORT"
notice): no OS secret store in a container, so it mints a portable key
and encrypts the tokens with it. Seal it alongside; the Secret is the
protection for both either way.

## 3. Seal, clean up

```sh
D=$(mktemp -d); chmod 700 $D
kubectl exec -n media libation-login -- cat /config/AccountsSettings.json > $D/AccountsSettings.json
kubectl exec -n media libation-login -- cat /config/libation-master.key > $D/libation-master.key
kubectl create secret generic libation -n media --dry-run=client -o yaml \
  --from-file=AccountsSettings.json=$D/AccountsSettings.json \
  --from-file=libation-master.key=$D/libation-master.key \
  | kubeseal --cert talos/clusters/homelab/sealed-secrets.crt -o yaml \
  > k8s/apps/libation/sealed-secret.yaml
rm -rf $D
kubectl delete pod -n media libation-login
```

Commit, push; ArgoCD applies the new Secret and the next Job uses it.
A first run by hand: `kubectl create job -n media --from=cronjob/libation libation-manual`.
