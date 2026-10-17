package com.teacherkevin.altvoiceinput;

import android.app.Activity;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.view.Window;
import android.view.WindowManager;
import android.webkit.JavascriptInterface;
import android.webkit.PermissionRequest;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;

public class MainActivity extends Activity {
    private WebView webView;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        requestWindowFeature(Window.FEATURE_NO_TITLE);
        if (Build.VERSION.SDK_INT >= 21) {
            Window window = getWindow();
            window.addFlags(WindowManager.LayoutParams.FLAG_DRAWS_SYSTEM_BAR_BACKGROUNDS);
            window.setStatusBarColor(Color.parseColor("#0F111A"));
            window.setNavigationBarColor(Color.parseColor("#0F111A"));
        }

        if (Build.VERSION.SDK_INT >= 23) {
            if (checkSelfPermission("android.permission.RECORD_AUDIO") != PackageManager.PERMISSION_GRANTED) {
                requestPermissions(new String[]{"android.permission.RECORD_AUDIO"}, 101);
            }
        }

        webView = new WebView(this);
        webView.setBackgroundColor(Color.parseColor("#0F111A"));
        WebSettings settings = webView.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setDatabaseEnabled(true);
        settings.setAllowFileAccess(true);
        settings.setAllowContentAccess(true);
        settings.setAllowFileAccessFromFileURLs(true);
        settings.setAllowUniversalAccessFromFileURLs(true);
        settings.setMediaPlaybackRequiresUserGesture(false);

        webView.addJavascriptInterface(new AndroidBridge(this), "AndroidBridge");
        webView.setWebViewClient(new AppWebViewClient(this));
        webView.setWebChromeClient(new AppWebChromeClient(this));

        setContentView(webView);
        webView.loadUrl("file:///android_asset/index.html");
    }

    public static class AppWebViewClient extends WebViewClient {
        private final Activity act;
        public AppWebViewClient(Activity a) {
            this.act = a;
        }
        @Override
        public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
            Uri uri = request.getUrl();
            String scheme = uri.getScheme();
            if ("http".equalsIgnoreCase(scheme) || "https".equalsIgnoreCase(scheme)) {
                Intent i = new Intent(Intent.ACTION_VIEW, uri);
                act.startActivity(i);
                return true;
            }
            return false;
        }
    }

    public static class GrantRunnable implements Runnable {
        private final PermissionRequest req;
        public GrantRunnable(PermissionRequest r) {
            this.req = r;
        }
        @Override
        public void run() {
            req.grant(req.getResources());
        }
    }

    public static class AppWebChromeClient extends WebChromeClient {
        private final Activity act;
        public AppWebChromeClient(Activity a) {
            this.act = a;
        }
        @Override
        public void onPermissionRequest(PermissionRequest request) {
            act.runOnUiThread(new GrantRunnable(request));
        }
    }

    public static class AndroidBridge {
        private final Context ctx;
        public AndroidBridge(Context c) {
            this.ctx = c;
        }
        @JavascriptInterface
        public void copyText(String text) {
            ClipboardManager cb = (ClipboardManager) ctx.getSystemService(Context.CLIPBOARD_SERVICE);
            if (cb != null) {
                cb.setPrimaryClip(ClipData.newPlainText("AltVoiceInput", text));
            }
        }
        @JavascriptInterface
        public void shareText(String text) {
            Intent sendIntent = new Intent();
            sendIntent.setAction(Intent.ACTION_SEND);
            sendIntent.putExtra(Intent.EXTRA_TEXT, text);
            sendIntent.setType("text/plain");
            Intent shareIntent = Intent.createChooser(sendIntent, "分享潤飾文字");
            shareIntent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            ctx.startActivity(shareIntent);
        }
    }

    @Override
    public void onBackPressed() {
        if (webView != null && webView.canGoBack()) {
            webView.goBack();
        } else {
            super.onBackPressed();
        }
    }
}
