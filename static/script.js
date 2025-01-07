let errorMessage = message => {
    document.getElementById('errorMessages').textContent = message;
    document.getElementById('errorMessages').hidden = false;
    document.getElementById('successMessages').textContent = '';
    document.getElementById('successMessages').hidden = true;
};

let successMessage = message => {
    document.getElementById('errorMessages').textContent = '';
    document.getElementById('errorMessages').hidden = true;
    document.getElementById('successMessages').textContent = message;
    document.getElementById('successMessages').hidden = false;
};

let hideMessages = () => {
    document.getElementById('errorMessages').hidden = true;
    document.getElementById('successMessages').hidden = true;
};

let preformattedMessage = message => {
    document.getElementById('preformattedMessages').textContent = message;
};

let browserCheck = () => {
    if (!window.PublicKeyCredential) {
        errorMessage('This browser does not support WebAuthn :(');
        return false;
    }

    return true;
};

// base64url > base64 > Uint8Array > ArrayBuffer
let bufferDecode = value => Uint8Array.from(atob(value.replace(/-/g, "+").replace(/_/g, "/")), c => c.charCodeAt(0))
    .buffer;

// ArrayBuffer > Uint8Array > base64 > base64url
let bufferEncode = value => btoa(String.fromCharCode.apply(null, new Uint8Array(value)))
    .replace(/\+/g, "-").replace(/\//g, "_").replace(/=/g, "");

let formatFinishRegParams = cred => JSON.stringify({
    id: cred.id,
    rawId: bufferEncode(cred.rawId),
    type: cred.type,
    response: {
        attestationObject: bufferEncode(cred.response.attestationObject),
        clientDataJSON: bufferEncode(cred.response.clientDataJSON),
    },
});

let formatFinishLoginParams = assertion => JSON.stringify({
    id: assertion.id,
    rawId: bufferEncode(assertion.rawId),
    type: assertion.type,
    response: {
        authenticatorData: bufferEncode(assertion.response.authenticatorData),
        clientDataJSON: bufferEncode(assertion.response.clientDataJSON),
        signature: bufferEncode(assertion.response.signature),
        userHandle: bufferEncode(assertion.response.userHandle),
    }
});

let registerUser = () => {
    let usernameInput = document.getElementById('username');
    let username = usernameInput.value;

    if (username === '') {
        errorMessage('Please enter a valid username');
        return;
    }

    fetch(`/webauthn/register/get_credential_creation_options?username=${encodeURIComponent(username)}`)
        .then(response => response.json())
        .then(credCreateOptions => {
            credCreateOptions.publicKey.challenge = bufferDecode(credCreateOptions.publicKey.challenge);
            credCreateOptions.publicKey.user.id = bufferDecode(credCreateOptions.publicKey.user.id);
            if (credCreateOptions.publicKey.excludeCredentials) {
                for (let cred of credCreateOptions.publicKey.excludeCredentials) {
                    cred.id = bufferDecode(cred.id);
                }
            }

            return navigator.credentials.create({
                publicKey: credCreateOptions.publicKey
            });
        })
        .then(cred => fetch(`/webauthn/register/process_registration_attestation?username=${encodeURIComponent(username)}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: formatFinishRegParams(cred)
        }))
        .then(response => response.json())
        .then(success => {
            successMessage(success.Message);
            preformattedMessage(success.Data);
        })
        .catch(error => {
            if (error.responseJSON) {
                errorMessage(error.responseJSON.Message);
            } else {
                errorMessage(error);
            }
        });
};

let authenticateUser = () => {
    let usernameInput = document.getElementById('username');
    let username = usernameInput.value;

    if (username === '') {
        errorMessage('Please enter a valid username');
        return;
    }

    fetch(`/webauthn/login/get_credential_request_options?username=${encodeURIComponent(username)}`)
        .then(response => response.json())
        .then(credRequestOptions => {
            credRequestOptions.publicKey.challenge = bufferDecode(credRequestOptions.publicKey.challenge);
            credRequestOptions.publicKey.allowCredentials.forEach(listItem => {
                listItem.id = bufferDecode(listItem.id);
            });

            return navigator.credentials.get({
                publicKey: credRequestOptions.publicKey
            });
        })
        .then(assertion => fetch(`/webauthn/login/process_login_assertion?username=${encodeURIComponent(username)}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: formatFinishLoginParams(assertion)
        }))
        .then(response => response.json())
        .then(success => {
            successMessage(success.Message);
            window.location.reload();
        })
        .catch(error => {
            if (error.responseJSON) {
                errorMessage(error.responseJSON.Message);
            } else {
                errorMessage(error);
            }
        });
};
